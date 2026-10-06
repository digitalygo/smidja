package tui

import (
	"context"
	"errors"
	"os/exec"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"
)

const maxLinkTargetLength = 2048

var ErrUnsafeLink = errors.New("tui: link target is not allowed")

type LinkOpener interface {
	Open(ctx context.Context, target string) error
}

type LinkOpenerFunc func(ctx context.Context, target string) error

func (f LinkOpenerFunc) Open(ctx context.Context, target string) error {
	return f(ctx, target)
}

type CommandLinkOpener struct {
	Timeout time.Duration
	Command string
}

func (o *CommandLinkOpener) Open(ctx context.Context, target string) error {
	if !IsSafeLinkTarget(target) {
		return ErrUnsafeLink
	}
	command := o.Command
	if command == "" {
		if runtime.GOOS == "darwin" {
			command = "open"
		} else {
			command = "xdg-open"
		}
	}
	timeout := o.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, command, target)
	if err := cmd.Start(); err != nil {
		return err
	}
	_ = cmd.Wait()
	return nil
}

func IsSafeLinkTarget(target string) bool {
	trimmed := strings.TrimSpace(target)
	if trimmed == "" || len(trimmed) > maxLinkTargetLength {
		return false
	}
	if strings.ContainsFunc(trimmed, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return false
	}
	lower := strings.ToLower(trimmed)
	for _, scheme := range []string{"http://", "https://", "mailto:"} {
		if strings.HasPrefix(lower, scheme) {
			return len(trimmed) > len(scheme)
		}
	}
	return false
}

type LinkHit struct {
	Row   int
	Start int
	End   int
	URL   string
}

func ExtractLinkHits(line string, row int) []LinkHit {
	var hits []LinkHit
	column := 0
	activeURL := ""
	activeStart := 0
	index := 0
	for index < len(line) {
		if target, size, isOpen, ok := parseOSC8(line, index); ok {
			if isOpen {
				if activeURL == "" {
					activeURL = target
					activeStart = column
				}
			} else if activeURL != "" {
				hits = append(hits, LinkHit{Row: row, Start: activeStart, End: column, URL: activeURL})
				activeURL = ""
			}
			index += size
			continue
		}
		if code, ok := extractANSI(line, index); ok {
			index += code.length
			continue
		}
		r, size := utf8.DecodeRuneInString(line[index:])
		column += runeWidth(r)
		index += size
	}
	if activeURL != "" && column > activeStart {
		hits = append(hits, LinkHit{Row: row, Start: activeStart, End: column, URL: activeURL})
	}
	return hits
}

func parseOSC8(line string, index int) (target string, size int, isOpen bool, ok bool) {
	const prefix = "\x1b]8;"
	if !strings.HasPrefix(line[index:], prefix) {
		return "", 0, false, false
	}
	rest := line[index+len(prefix):]
	terminator := strings.IndexByte(rest, '\x07')
	terminatorSize := 1
	if stIndex := strings.Index(rest, "\x1b\\"); stIndex >= 0 && (terminator < 0 || stIndex < terminator) {
		terminator = stIndex
		terminatorSize = 2
	}
	if terminator < 0 {
		return "", 0, false, false
	}
	params := rest[:terminator]
	semicolon := strings.IndexByte(params, ';')
	if semicolon < 0 {
		return "", 0, false, false
	}
	target = params[semicolon+1:]
	return target, len(prefix) + terminator + terminatorSize, target != "", true
}

func LinkAt(line string, row, column int) (string, bool) {
	for _, hit := range ExtractLinkHits(line, row) {
		if column >= hit.Start && column < hit.End {
			target := SanitizeOSC8Target(hit.URL)
			if IsSafeLinkTarget(target) {
				return target, true
			}
		}
	}
	return "", false
}

func SanitizeOSC8Target(target string) string {
	var builder strings.Builder
	for _, r := range target {
		if r < 0x20 || r == 0x7f {
			continue
		}
		builder.WriteRune(r)
	}
	return builder.String()
}
