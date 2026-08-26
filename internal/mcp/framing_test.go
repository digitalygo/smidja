package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"testing"
)

func TestNDJSONRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	io := newFrameIO(&buf, &buf, FramingNDJSON, FramingNDJSON)
	req := Request{Jsonrpc: JsonRPCVersion, ID: []byte("7"), Method: "ping"}
	if err := io.Write(req); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if !strings.HasSuffix(buf.String(), "\n") {
		t.Fatalf("ndjson frame missing newline: %q", buf.String())
	}
	raw, err := io.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	var got Request
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Method != "ping" || string(got.ID) != "7" {
		t.Fatalf("round trip = %+v", got)
	}
}

func TestContentLengthRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	io := newFrameIO(&buf, &buf, FramingContentLength, FramingContentLength)
	req := Request{Jsonrpc: JsonRPCVersion, ID: []byte("3"), Method: "tools/list", Params: []byte(`{"cursor":""}`)}
	if err := io.Write(req); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if !strings.HasPrefix(buf.String(), "Content-Length: ") {
		t.Fatalf("missing content-length header: %q", buf.String())
	}
	raw, err := io.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	var got Request
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Method != "tools/list" || string(got.ID) != "3" {
		t.Fatalf("round trip = %+v", got)
	}
}

func TestAutoDetectNDJSON(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteString("\n\t {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{}}\n")
	io := newFrameIO(&buf, io.Discard, FramingNDJSON, FramingAuto)
	raw, err := io.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !strings.Contains(string(raw), "result") {
		t.Fatalf("frame = %q", raw)
	}
	if io.Detected() != FramingNDJSON {
		t.Fatalf("detected framing = %v, want ndjson", io.Detected())
	}
}

func TestAutoDetectContentLength(t *testing.T) {
	body := `{"jsonrpc":"2.0","id":1}`
	var buf bytes.Buffer
	buf.WriteString("Content-Length: " + strconv.Itoa(len(body)) + "\r\n\r\n" + body)
	io := newFrameIO(&buf, io.Discard, FramingNDJSON, FramingAuto)
	raw, err := io.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if string(raw) != body {
		t.Fatalf("frame = %q", raw)
	}
	if io.Detected() != FramingContentLength {
		t.Fatalf("detected framing = %v, want content-length", io.Detected())
	}
}

func TestAutoDetectLowercaseContentLength(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteString("content-length: 2\r\n\r\n{}")
	io := newFrameIO(&buf, io.Discard, FramingNDJSON, FramingAuto)
	raw, err := io.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if string(raw) != "{}" {
		t.Fatalf("frame = %q", raw)
	}
}

func TestAutoDetectUnknownFraming(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteString("HTTP/1.1 400 Bad Request")
	io := newFrameIO(&buf, io.Discard, FramingNDJSON, FramingAuto)
	if _, err := io.Read(); !errors.Is(err, ErrUnknownFraming) {
		t.Fatalf("Read error = %v, want ErrUnknownFraming", err)
	}
}

func TestNDJSONSkipsBlankLines(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteString("\n\n\r\n{\"jsonrpc\":\"2.0\",\"id\":1}\n\n")
	io := newFrameIO(&buf, io.Discard, FramingNDJSON, FramingNDJSON)
	raw, err := io.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if string(raw) != `{"jsonrpc":"2.0","id":1}` {
		t.Fatalf("frame = %q", raw)
	}
}

func TestMultipleNDJSONFrames(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteString("{\"a\":1}\n{\"b\":2}\n{\"c\":3}\n")
	io := newFrameIO(&buf, io.Discard, FramingNDJSON, FramingNDJSON)
	for i, want := range []string{`{"a":1}`, `{"b":2}`, `{"c":3}`} {
		raw, err := io.Read()
		if err != nil {
			t.Fatalf("frame %d: %v", i, err)
		}
		if string(raw) != want {
			t.Fatalf("frame %d = %q, want %q", i, raw, want)
		}
	}
}

func TestMultipleContentLengthFrames(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteString("Content-Length: 5\r\n\r\nhelloContent-Length: 3\r\n\r\nabc")
	io := newFrameIO(&buf, io.Discard, FramingContentLength, FramingContentLength)
	first, err := io.Read()
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	if string(first) != "hello" {
		t.Fatalf("first = %q", first)
	}
	second, err := io.Read()
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if string(second) != "abc" {
		t.Fatalf("second = %q", second)
	}
}

func TestContentLengthExtraHeaders(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteString("X-Ignore: yes\r\nContent-Length: 2\r\n\r\n{}")
	io := newFrameIO(&buf, io.Discard, FramingContentLength, FramingContentLength)
	raw, err := io.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if string(raw) != "{}" {
		t.Fatalf("frame = %q", raw)
	}
}

func TestContentLengthMissingHeader(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteString("\r\n")
	io := newFrameIO(&buf, io.Discard, FramingContentLength, FramingContentLength)
	if _, err := io.Read(); !errors.Is(err, ErrMissingContentLength) {
		t.Fatalf("Read error = %v, want ErrMissingContentLength", err)
	}
}

func TestContentLengthInvalidValue(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteString("Content-Length: nope\r\n\r\n")
	io := newFrameIO(&buf, io.Discard, FramingContentLength, FramingContentLength)
	if _, err := io.Read(); !errors.Is(err, ErrInvalidContentLength) {
		t.Fatalf("Read error = %v, want ErrInvalidContentLength", err)
	}
}

func TestWriteFrameTooLarge(t *testing.T) {
	var buf bytes.Buffer
	io := newFrameIOLimited(&buf, io.Discard, FramingNDJSON, FramingNDJSON, 8, 8)
	if err := io.Write(map[string]string{"k": strings.Repeat("a", 20)}); !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("Write error = %v, want ErrFrameTooLarge", err)
	}
}

func TestReadFrameTooLarge(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteString(`{"big":"` + strings.Repeat("a", 64) + `"}` + "\n")
	io := newFrameIOLimited(&buf, io.Discard, FramingNDJSON, FramingNDJSON, 32, 32)
	if _, err := io.Read(); !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("Read error = %v, want ErrFrameTooLarge", err)
	}
}

func TestReadBodyTooLarge(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteString("Content-Length: 100\r\n\r\n" + strings.Repeat("x", 100))
	io := newFrameIOLimited(&buf, io.Discard, FramingContentLength, FramingContentLength, 32, 64)
	if _, err := io.Read(); !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("Read error = %v, want ErrFrameTooLarge", err)
	}
}

func TestHeaderTooLarge(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteString("X-Pad: " + strings.Repeat("a", 200) + "\r\nContent-Length: 1\r\n\r\n{}")
	io := newFrameIOLimited(&buf, io.Discard, FramingContentLength, FramingContentLength, 1024, 64)
	if _, err := io.Read(); !errors.Is(err, ErrHeaderTooLarge) {
		t.Fatalf("Read error = %v, want ErrHeaderTooLarge", err)
	}
}

func TestFragmentedNDJSONLine(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteString(`{"jsonrpc":"2.0","id":`)
	io := newFrameIO(&buf, io.Discard, FramingNDJSON, FramingNDJSON)
	buf.WriteString(`1,"result":{}}`)
	buf.WriteString("\n")
	raw, err := io.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !strings.Contains(string(raw), "result") {
		t.Fatalf("frame = %q", raw)
	}
}

func TestFragmentedContentLength(t *testing.T) {
	body := `{"jsonrpc":"2.0","id":1}`
	head := "Content-Length: " + strconv.Itoa(len(body)) + "\r\n\r\n"
	r := io.MultiReader(strings.NewReader(head+`{"jsonrpc":"2`), strings.NewReader(`.0","id":1}`))
	fio := newFrameIO(r, io.Discard, FramingContentLength, FramingContentLength)
	raw, err := fio.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(raw) != len(body) || string(raw) != body {
		t.Fatalf("frame = %q, want %q", raw, body)
	}
}

func TestContentLengthWriterBytes(t *testing.T) {
	var buf bytes.Buffer
	fio := newFrameIO(strings.NewReader(""), &buf, FramingContentLength, FramingContentLength)
	body := `{"jsonrpc":"2.0"}`
	if err := fio.Write(json.RawMessage(body)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	want := "Content-Length: " + strconv.Itoa(len(body)) + "\r\n\r\n" + body
	if buf.String() != want {
		t.Fatalf("wire bytes = %q, want %q", buf.String(), want)
	}
}

func TestDetectedReturnsAutoBeforeRead(t *testing.T) {
	fio := newFrameIO(strings.NewReader(""), io.Discard, FramingNDJSON, FramingAuto)
	if fio.Detected() != FramingAuto {
		t.Fatalf("Detected = %v before any read, want auto", fio.Detected())
	}
}
