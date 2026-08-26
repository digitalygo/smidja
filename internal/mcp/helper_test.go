package mcp

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	helperEnv         = "MCP_TEST_HELPER"
	helperEnvBehavior = "MCP_TEST_HELPER_BEHAVIOR"
	helperEnvFraming  = "MCP_TEST_HELPER_FRAMING"
	helperEnvMarker   = "MCP_TEST_HELPER_MARKER"
)

type trickleWriter struct {
	w io.Writer
}

func (t trickleWriter) Write(p []byte) (int, error) {
	for i := range p {
		if _, err := t.w.Write(p[i : i+1]); err != nil {
			return i, err
		}
		time.Sleep(time.Millisecond)
	}
	return len(p), nil
}

type helperServer struct {
	io       *frameIO
	behavior string
	marker   string
	start    int
	awaiting map[string]string
}

func TestHelperProcess(t *testing.T) {
	if os.Getenv(helperEnv) != "1" {
		return
	}
	os.Exit(runHelperProcess())
}

func runHelperProcess() int {
	behavior := os.Getenv(helperEnvBehavior)
	framing := os.Getenv(helperEnvFraming)
	marker := os.Getenv(helperEnvMarker)
	writeMode := FramingNDJSON
	readMode := FramingNDJSON
	switch framing {
	case "content-length":
		writeMode = FramingContentLength
		readMode = FramingContentLength
	case "auto-cl":
		writeMode = FramingContentLength
		readMode = FramingAuto
	case "auto":
		writeMode = FramingNDJSON
		readMode = FramingAuto
	}
	writer := io.Writer(os.Stdout)
	if behavior == "fragmented" {
		writer = &trickleWriter{w: os.Stdout}
	}
	srv := &helperServer{
		io:       newFrameIO(os.Stdin, writer, writeMode, readMode),
		behavior: behavior,
		marker:   marker,
		start:    helperStartCount(marker) + 1,
		awaiting: map[string]string{},
	}
	helperMarker(marker, "start:"+strconv.Itoa(srv.start))
	for {
		raw, err := srv.io.Read()
		if err != nil {
			return 0
		}
		var head struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if err := json.Unmarshal(raw, &head); err != nil {
			continue
		}
		if head.Method != "" && len(head.ID) == 0 {
			if head.Method == MethodInitialized {
				if code := srv.onInitialized(); code != 0 {
					return code
				}
			}
			continue
		}
		if head.Method != "" {
			if code := srv.handleRequest(head.ID, head.Method, raw); code != 0 {
				return code
			}
			continue
		}
		if code := srv.handleResponse(head.ID, raw); code != 0 {
			return code
		}
	}
}

func (s *helperServer) onInitialized() int {
	helperMarker(s.marker, "initialized:"+strconv.Itoa(s.start))
	if s.behavior == "stderr-log" {
		fmt.Fprintln(os.Stderr, "helper-stderr-line")
	}
	switch s.behavior {
	case "crash-always", "fail-on-respawn":
		return 1
	case "notify-list-changed":
		for i := 0; i < 5; i++ {
			s.io.Write(Notification{Jsonrpc: JsonRPCVersion, Method: NotifyToolsListChanged})
		}
		helperMarker(s.marker, "notify:5")
	case "ping-request":
		s.io.Write(Request{Jsonrpc: JsonRPCVersion, ID: json.RawMessage(`"srv-ping"`), Method: MethodPing})
		s.awaiting[`"srv-ping"`] = "ping"
		s.io.Write(Request{Jsonrpc: JsonRPCVersion, ID: json.RawMessage(`"srv-unknown"`), Method: "bogus/method"})
		s.awaiting[`"srv-unknown"`] = "unknown"
	}
	return 0
}

func (s *helperServer) handleRequest(id json.RawMessage, method string, raw json.RawMessage) int {
	switch method {
	case MethodInitialize:
		var req Request
		if err := json.Unmarshal(raw, &req); err != nil {
			return 1
		}
		var params InitializeParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return 1
		}
		version := params.ProtocolVersion
		if s.behavior == "version-fixed" || s.behavior == "fail-on-respawn" && s.start >= 2 {
			version = "1900-01-01"
		}
		if s.behavior == "version-other" {
			version = "2024-11-05"
		}
		result := mustMarshal(InitializeResult{
			ProtocolVersion: version,
			Capabilities:    ServerCapabilities{Tools: &ToolsCapability{ListChanged: true}},
			ServerInfo:      ServerInfo{Name: "mcp-test-helper", Version: "1.0"},
		})
		if s.behavior == "notools-cap" {
			result = mustMarshal(InitializeResult{
				ProtocolVersion: version,
				ServerInfo:      ServerInfo{Name: "mcp-test-helper", Version: "1.0"},
			})
		}
		if s.behavior == "no-serverinfo" {
			result = mustMarshal(InitializeResult{
				ProtocolVersion: version,
				Capabilities:    ServerCapabilities{Tools: &ToolsCapability{ListChanged: true}},
			})
		}
		if s.behavior == "bad-init" {
			return writeOrFail(s.io, Response{Jsonrpc: JsonRPCVersion, ID: id, Result: json.RawMessage(`not json`)})
		}
		return writeOrFail(s.io, Response{Jsonrpc: JsonRPCVersion, ID: id, Result: result})
	case MethodToolsList:
		if s.behavior == "crash-on-list" && s.start == 1 {
			helperMarker(s.marker, "crash-on-list:1")
			return 1
		}
		helperMarker(s.marker, "list:"+strconv.Itoa(s.start))
		var params ListToolsParams
		var req Request
		if err := json.Unmarshal(raw, &req); err != nil {
			return 1
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return 1
		}
		return writeOrFail(s.io, s.listResponse(id, params.Cursor))
	case MethodToolsCall:
		helperMarker(s.marker, "call:"+strconv.Itoa(s.start))
		var req Request
		if err := json.Unmarshal(raw, &req); err != nil {
			return 1
		}
		var params CallToolParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return 1
		}
		return s.callResponse(id, params)
	case MethodPing:
		return writeOrFail(s.io, Response{Jsonrpc: JsonRPCVersion, ID: id, Result: json.RawMessage("{}")})
	default:
		return writeOrFail(s.io, Response{Jsonrpc: JsonRPCVersion, ID: id, Error: &RPCError{Code: CodeMethodNotFound, Message: "method not found"}})
	}
}

func (s *helperServer) handleResponse(id json.RawMessage, raw json.RawMessage) int {
	method, ok := s.awaiting[string(id)]
	if !ok {
		return 0
	}
	delete(s.awaiting, method)
	var resp Response
	if err := json.Unmarshal(raw, &resp); err != nil {
		return 1
	}
	switch method {
	case "ping":
		if resp.Error != nil || string(resp.Result) != "{}" {
			return 1
		}
		helperMarker(s.marker, "ping:ok")
	case "unknown":
		if resp.Error == nil || resp.Error.Code != CodeMethodNotFound {
			return 1
		}
		helperMarker(s.marker, "unknown:ok")
	}
	return 0
}

func (s *helperServer) listResponse(id json.RawMessage, cursor string) Response {
	switch s.behavior {
	case "paged":
		pages := [][]ToolInfo{
			helperTools()[:2],
			helperTools()[2:4],
			helperTools()[4:],
		}
		idx := 0
		switch cursor {
		case "p2":
			idx = 2
		case "p1":
			idx = 1
		}
		next := ""
		if idx < 2 {
			next = fmt.Sprintf("p%d", idx+1)
		}
		return Response{Jsonrpc: JsonRPCVersion, ID: id, Result: mustMarshal(ListToolsResult{Tools: pages[idx], NextCursor: next})}
	case "toomany":
		page := make([]ToolInfo, 110)
		for i := range page {
			page[i] = ToolInfo{Name: "t" + strconv.Itoa(i), InputSchema: json.RawMessage(`{"type":"object"}`)}
		}
		next := ""
		if cursor == "" {
			next = "more"
		}
		return Response{Jsonrpc: JsonRPCVersion, ID: id, Result: mustMarshal(ListToolsResult{Tools: page, NextCursor: next})}
	case "collide":
		return Response{Jsonrpc: JsonRPCVersion, ID: id, Result: mustMarshal(ListToolsResult{Tools: []ToolInfo{
			{Name: "a.b", InputSchema: json.RawMessage(`{"type":"object"}`)},
			{Name: "a b", InputSchema: json.RawMessage(`{"type":"object"}`)},
		}})}
	case "badschema":
		return Response{Jsonrpc: JsonRPCVersion, ID: id, Result: mustMarshal(ListToolsResult{Tools: []ToolInfo{
			{Name: "bad", InputSchema: json.RawMessage(`null`)},
		}})}
	case "bigschema":
		return Response{Jsonrpc: JsonRPCVersion, ID: id, Result: mustMarshal(ListToolsResult{Tools: []ToolInfo{
			{Name: "big", InputSchema: json.RawMessage(`{"type":"object","properties":{"x":{"type":"string","description":"` + strings.Repeat("a", 300*1024) + `"}}}`)},
		}})}
	case "noschema":
		return Response{Jsonrpc: JsonRPCVersion, ID: id, Result: mustMarshal(ListToolsResult{Tools: []ToolInfo{
			{Name: "noschema", InputSchema: json.RawMessage(`[]`)},
		}})}
	case "notools":
		return Response{Jsonrpc: JsonRPCVersion, ID: id, Result: mustMarshal(ListToolsResult{Tools: []ToolInfo{}})}
	default:
		return Response{Jsonrpc: JsonRPCVersion, ID: id, Result: mustMarshal(ListToolsResult{Tools: helperTools()})}
	}
}

func (s *helperServer) callResponse(id json.RawMessage, params CallToolParams) int {
	if s.behavior == "crash-after-call" && s.start == 1 {
		helperMarker(s.marker, "crash:call:1")
		return 1
	}
	if s.behavior == "notify-list-changed" {
		for i := 0; i < 3; i++ {
			s.io.Write(Notification{Jsonrpc: JsonRPCVersion, Method: NotifyToolsListChanged})
		}
		helperMarker(s.marker, "notify:3")
	}
	switch s.behavior {
	case "crash-always":
		return 1
	case "call-error":
		return writeOrFail(s.io, Response{Jsonrpc: JsonRPCVersion, ID: id, Error: &RPCError{Code: CodeInvalidParams, Message: "bad params"}})
	}
	switch params.Name {
	case "slow":
		time.Sleep(25 * time.Millisecond)
		return writeOrFail(s.io, Response{Jsonrpc: JsonRPCVersion, ID: id, Result: mustMarshal(CallToolResult{
			Content: []CallContent{{Type: "text", Text: string(params.Arguments)}},
		})})
	case "fail":
		return writeOrFail(s.io, Response{Jsonrpc: JsonRPCVersion, ID: id, Result: mustMarshal(CallToolResult{
			IsError: true,
			Content: []CallContent{{Type: "text", Text: "boom"}},
		})})
	case "rich":
		return writeOrFail(s.io, Response{Jsonrpc: JsonRPCVersion, ID: id, Result: mustMarshal(CallToolResult{
			Content: []CallContent{{Type: "image", Text: ""}},
		})})
	case "jsonify":
		return writeOrFail(s.io, Response{Jsonrpc: JsonRPCVersion, ID: id, Result: mustMarshal(CallToolResult{
			Content: []CallContent{{Type: "json", JSON: json.RawMessage(`{"k":"v"}`)}},
		})})
	case "mixed":
		return writeOrFail(s.io, Response{Jsonrpc: JsonRPCVersion, ID: id, Result: mustMarshal(CallToolResult{
			Content: []CallContent{
				{Type: "text", Text: "first"},
				{Type: "json", JSON: json.RawMessage(`{"n":1}`)},
			},
		})})
	default:
		return writeOrFail(s.io, Response{Jsonrpc: JsonRPCVersion, ID: id, Result: mustMarshal(CallToolResult{
			Content: []CallContent{{Type: "text", Text: string(params.Arguments)}},
		})})
	}
}

func helperTools() []ToolInfo {
	return []ToolInfo{
		{Name: "echo", Description: "echo arguments", InputSchema: json.RawMessage(`{"type":"object","properties":{"value":{"type":"string"}},"required":["value"]}`)},
		{Name: "fail", Description: "always fails", InputSchema: json.RawMessage(`{"type":"object"}`)},
		{Name: "rich", Description: "rich content", InputSchema: json.RawMessage(`{"type":"object"}`)},
		{Name: "jsonify", Description: "json content", InputSchema: json.RawMessage(`{"type":"object"}`)},
		{Name: "mixed", Description: "mixed content", InputSchema: json.RawMessage(`{"type":"object"}`)},
		{Name: "slow", Description: "slow echo", InputSchema: json.RawMessage(`{"type":"object"}`)},
	}
}

func writeOrFail(io *frameIO, resp Response) int {
	if err := io.Write(resp); err != nil {
		return 1
	}
	return 0
}

func helperMarker(path, line string) {
	if path == "" {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	fmt.Fprintln(f, line)
	f.Close()
}

func helperStartCount(path string) int {
	if path == "" {
		return 0
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	count := 0
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "start:") {
			count++
		}
	}
	return count
}

func readMarkerLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var lines []string
	for _, line := range strings.Split(string(data), "\n") {
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func waitMarkerLine(t *testing.T, path, prefix string) {
	t.Helper()
	waitMarkerCount(t, path, prefix, 1)
}

func waitMarkerCount(t *testing.T, path, prefix string, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if countPrefix(readMarkerLines(t, path), prefix) >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("marker %s: %d lines with prefix %q never appeared; got %v", path, want, prefix, readMarkerLines(t, path))
}
