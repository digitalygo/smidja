package mcp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"sync"
)

type Framing int

const (
	FramingAuto Framing = iota
	FramingNDJSON
	FramingContentLength
)

var (
	ErrFrameTooLarge        = errors.New("mcp: frame exceeds size limit")
	ErrHeaderTooLarge       = errors.New("mcp: frame header exceeds size limit")
	ErrMissingContentLength = errors.New("mcp: frame missing Content-Length header")
	ErrInvalidContentLength = errors.New("mcp: frame has invalid Content-Length header")
	ErrUnknownFraming       = errors.New("mcp: cannot determine framing from peer bytes")
)

var (
	defaultMaxFrameBytes  int64 = 8 * 1024 * 1024
	defaultMaxHeaderBytes int64 = 64 * 1024
)

type frameIO struct {
	writeMu   sync.Mutex
	writeMode Framing
	readMode  Framing
	reader    *bufio.Reader
	writer    io.Writer
	maxBody   int64
	maxHeader int64
}

func newFrameIO(r io.Reader, w io.Writer, writeMode, readMode Framing) *frameIO {
	return newFrameIOLimited(r, w, writeMode, readMode, defaultMaxFrameBytes, defaultMaxHeaderBytes)
}

func newFrameIOLimited(r io.Reader, w io.Writer, writeMode, readMode Framing, maxBody, maxHeader int64) *frameIO {
	return &frameIO{
		writeMode: writeMode,
		readMode:  readMode,
		reader:    bufio.NewReader(r),
		writer:    w,
		maxBody:   maxBody,
		maxHeader: maxHeader,
	}
}

func (f *frameIO) Detected() Framing {
	if f.readMode == FramingAuto {
		return FramingAuto
	}
	return f.readMode
}

func (f *frameIO) Write(v any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("mcp: marshal frame: %w", err)
	}
	if int64(len(body)) > f.maxBody {
		return ErrFrameTooLarge
	}
	f.writeMu.Lock()
	defer f.writeMu.Unlock()
	switch f.writeMode {
	case FramingNDJSON:
		_, err = f.writer.Write(append(body, '\n'))
	case FramingContentLength:
		header := strconv.AppendInt([]byte("Content-Length: "), int64(len(body)), 10)
		header = append(header, '\r', '\n', '\r', '\n')
		if _, err = f.writer.Write(header); err == nil {
			_, err = f.writer.Write(body)
		}
	default:
		err = fmt.Errorf("mcp: invalid write framing %d", f.writeMode)
	}
	if err != nil {
		return fmt.Errorf("mcp: write frame: %w", err)
	}
	return nil
}

func (f *frameIO) Read() (json.RawMessage, error) {
	if f.readMode == FramingAuto {
		if err := f.detect(); err != nil {
			return nil, err
		}
	}
	switch f.readMode {
	case FramingNDJSON:
		return f.readNDJSON()
	case FramingContentLength:
		return f.readContentLength()
	}
	return nil, fmt.Errorf("mcp: invalid read framing %d", f.readMode)
}

func (f *frameIO) detect() error {
	for {
		b, err := f.reader.Peek(1)
		if err != nil {
			return err
		}
		if !isJSONWhitespace(b[0]) {
			break
		}
		f.reader.ReadByte()
	}
	first, err := f.reader.Peek(1)
	if err != nil {
		return err
	}
	switch {
	case first[0] == '{' || first[0] == '[':
		f.readMode = FramingNDJSON
		return nil
	case first[0] == 'c' || first[0] == 'C':
		probe, err := f.reader.Peek(len("Content-Length:"))
		if err != nil {
			return fmt.Errorf("mcp: detect framing: %w", err)
		}
		if bytes.EqualFold(probe, []byte("Content-Length:")) {
			f.readMode = FramingContentLength
			return nil
		}
	}
	return ErrUnknownFraming
}

func (f *frameIO) readNDJSON() (json.RawMessage, error) {
	for {
		line, err := readLineLimited(f.reader, f.maxBody, ErrFrameTooLarge)
		if err != nil {
			return nil, err
		}
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		return json.RawMessage(line), nil
	}
}

func (f *frameIO) readContentLength() (json.RawMessage, error) {
	for {
		size, err := f.readContentLengthHeader()
		if err != nil {
			return nil, err
		}
		if size > f.maxBody {
			return nil, ErrFrameTooLarge
		}
		body := make([]byte, size)
		if _, err := io.ReadFull(f.reader, body); err != nil {
			return nil, err
		}
		return json.RawMessage(body), nil
	}
}

func (f *frameIO) readContentLengthHeader() (int64, error) {
	var total int64
	size := int64(-1)
	for {
		line, err := readLineLimited(f.reader, f.maxHeader, ErrHeaderTooLarge)
		if err != nil {
			return 0, err
		}
		total += int64(len(line))
		if total > f.maxHeader {
			return 0, ErrHeaderTooLarge
		}
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			if size < 0 {
				return 0, ErrMissingContentLength
			}
			return size, nil
		}
		key, value, ok := bytes.Cut(line, []byte(":"))
		if !ok || !bytes.EqualFold(bytes.TrimSpace(key), []byte("Content-Length")) {
			continue
		}
		parsed, err := strconv.ParseInt(string(bytes.TrimSpace(value)), 10, 64)
		if err != nil || parsed < 0 {
			return 0, ErrInvalidContentLength
		}
		size = parsed
	}
}

func isJSONWhitespace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\r' || b == '\n'
}

func readLineLimited(r *bufio.Reader, limit int64, overflowErr error) ([]byte, error) {
	buf := make([]byte, 0, 256)
	for {
		chunk, err := r.ReadSlice('\n')
		buf = append(buf, chunk...)
		if int64(len(buf)) > limit {
			return nil, overflowErr
		}
		if err == nil {
			return buf, nil
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		return buf, err
	}
}
