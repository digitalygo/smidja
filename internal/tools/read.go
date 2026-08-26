package tools

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/digitalygo/smidja/internal/agent"
	"github.com/digitalygo/smidja/internal/workspace"
	"io"
	"os"
	"strings"
)

type readArgs struct {
	Path   *string `json:"path"`
	Offset *int    `json:"offset"`
	Limit  *int    `json:"limit"`
}

type readTool struct {
	ws       *workspace.Workspace
	maxLines int
	maxBytes int64
}

func (t *readTool) Name() string { return "read" }

func (t *readTool) Description() string {
	return "Reads a text file inside the workspace and returns its lines numbered like cat -n, with an optional 1-based offset and limit for paging; binary files are rejected and output is truncated to 2000 lines or 50 KB (whichever is hit first), with the full content saved to a temp file when truncated."
}

func (t *readTool) Schema() json.RawMessage {
	return schema(
		map[string]any{
			"path":   strProp("Path to the file to read, relative to the workspace root."),
			"offset": intProp("1-based line number of the first line to return. Defaults to 1."),
			"limit":  intProp("Maximum number of lines to return. Defaults to returning the rest of the file."),
		},
		"path",
	)
}

func (t *readTool) Exec(ctx context.Context, args json.RawMessage) Result {
	var a readArgs
	if res := decodeArgs("read", args, &a); res.IsError {
		return res
	}
	if a.Path == nil {
		return agent.ErrorResult("read: missing required argument 'path'")
	}
	full, res := contain(t.ws, *a.Path)
	if res.IsError {
		return res
	}
	offset := 1
	if a.Offset != nil {
		offset = *a.Offset
	}
	if offset < 1 {
		return agent.ErrorResult(fmt.Sprintf("read: offset must be a 1-based line number, got %d", offset))
	}
	limit := 0
	if a.Limit != nil {
		limit = *a.Limit
		if limit < 0 {
			return agent.ErrorResult(fmt.Sprintf("read: limit must be >= 0, got %d", limit))
		}
	}

	f, err := os.Open(full)
	if err != nil {
		return agent.ErrorResult(fmt.Sprintf("read %q: %v", *a.Path, err))
	}
	defer f.Close()

	st, err := f.Stat()
	if err != nil {
		return agent.ErrorResult(fmt.Sprintf("read %q: %v", *a.Path, err))
	}
	if st.IsDir() {
		return agent.ErrorResult(fmt.Sprintf("read %q: is a directory", *a.Path))
	}

	probe := make([]byte, 512)
	n, _ := io.ReadFull(f, probe)
	if bytes.IndexByte(probe[:n], 0) >= 0 {
		return agent.ErrorResult("[binary file]")
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return agent.ErrorResult(fmt.Sprintf("read %q: %v", *a.Path, err))
	}

	total, err := countLines(f)
	if err != nil {
		return agent.ErrorResult(fmt.Sprintf("read %q: %v", *a.Path, err))
	}
	if offset > total {
		return agent.ErrorResult(fmt.Sprintf("read %q: offset %d is beyond the end of the file (%d lines)", *a.Path, offset, total))
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return agent.ErrorResult(fmt.Sprintf("read %q: %v", *a.Path, err))
	}

	br := bufio.NewReaderSize(f, 64<<10)
	end := total
	if limit > 0 && offset+limit-1 < end {
		end = offset + limit - 1
	}
	for ln := 1; ln < offset; ln++ {
		if err := skipLine(br); err != nil {
			return agent.ErrorResult(fmt.Sprintf("read %q: %v", *a.Path, err))
		}
	}

	var sb strings.Builder
	var rawBuf bytes.Buffer
	var tmp *os.File
	var tmpPath string
	keptBytes := int64(0)
	lastShown := 0
	truncatedBy := ""
	firstLineExceeds := false
	lineExceedsSize := int64(0)

	defer func() {
		if tmp != nil {
			_ = tmp.Close()
		}
	}()

	ensureTemp := func() error {
		if tmp != nil {
			return nil
		}
		f, err := os.CreateTemp("", "smidja-read-*.log")
		if err != nil {
			return fmt.Errorf("read %q: create temp output file: %v", *a.Path, err)
		}
		tmp = f
		tmpPath = f.Name()
		if _, err := f.Write(rawBuf.Bytes()); err != nil {
			return fmt.Errorf("read %q: write temp output file: %v", *a.Path, err)
		}
		rawBuf.Reset()
		return nil
	}

	for ln := offset; ln <= end; ln++ {
		line, err := readLineRaw(br)
		if err != nil && !errors.Is(err, io.EOF) {
			return agent.ErrorResult(fmt.Sprintf("read %q: %v", *a.Path, err))
		}
		if tmp != nil {
			if _, err := tmp.Write(line); err != nil {
				return agent.ErrorResult(fmt.Sprintf("read %q: write temp output file: %v", *a.Path, err))
			}
		} else {
			rawBuf.Write(line)
		}

		body := line
		if len(body) > 0 && body[len(body)-1] == '\n' {
			body = body[:len(body)-1]
		}
		if truncatedBy == "" && !firstLineExceeds {
			switch {
			case ln == offset && int64(len(body)) > t.maxBytes:
				firstLineExceeds = true
				lineExceedsSize = int64(len(body))
			case lastShown >= offset+t.maxLines-1:
				truncatedBy = "lines"
			default:
				lineBytes := int64(len(body))
				if keptBytes > 0 {
					lineBytes++
				}
				if keptBytes+lineBytes > t.maxBytes {
					truncatedBy = "bytes"
					break
				}
				fmt.Fprintf(&sb, "%6d\t", ln)
				sb.Write(body)
				sb.WriteByte('\n')
				lastShown = ln
				keptBytes += lineBytes
			}
		}
		if truncatedBy != "" || firstLineExceeds {
			if err := ensureTemp(); err != nil {
				return agent.ErrorResult(err.Error())
			}
		}
	}

	if total == 0 {
		return agent.TextResult("")
	}

	out := sb.String()
	switch {
	case firstLineExceeds:
		return agent.TextResult(fmt.Sprintf(
			"[Line %d is %s, exceeds %s limit. Use bash: sed -n '%dp' %s | head -c %d. Full output: %s]",
			offset, formatSize(lineExceedsSize), formatSize(t.maxBytes), offset, *a.Path, t.maxBytes, tmpPath))
	case truncatedBy == "lines":
		return agent.TextResult(out + fmt.Sprintf(
			"\n[Showing lines %d-%d of %d. Use offset=%d to continue. Full output: %s]",
			offset, lastShown, total, lastShown+1, tmpPath))
	case truncatedBy == "bytes":
		return agent.TextResult(out + fmt.Sprintf(
			"\n[Showing lines %d-%d of %d (%s limit). Use offset=%d to continue. Full output: %s]",
			offset, lastShown, total, formatSize(t.maxBytes), lastShown+1, tmpPath))
	default:
		return agent.TextResult(out)
	}
}

func readLineRaw(br *bufio.Reader) ([]byte, error) {
	var out []byte
	for {
		chunk, err := br.ReadSlice('\n')
		out = append(out, chunk...)
		if err == bufio.ErrBufferFull {
			continue
		}
		return out, err
	}
}

func skipLine(br *bufio.Reader) error {
	for {
		_, err := br.ReadSlice('\n')
		if err == nil {
			return nil
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
}

func countLines(r io.Reader) (int, error) {
	br := bufio.NewReaderSize(r, 64<<10)
	total := 0
	readAny := false
	lastByteNewline := true
	for {
		b, err := br.ReadByte()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return 0, err
		}
		readAny = true
		if b == '\n' {
			total++
			lastByteNewline = true
		} else {
			lastByteNewline = false
		}
	}
	if readAny && !lastByteNewline {
		total++
	}
	return total, nil
}
