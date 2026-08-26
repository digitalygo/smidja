package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"testing"
)

const mcpHelperEnv = "MCP_CLI_HELPER"

func TestMCPHelperProcess(t *testing.T) {
	if os.Getenv(mcpHelperEnv) != "1" {
		return
	}
	os.Exit(runMCPHelper())
}

func runMCPHelper() int {
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		var head struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if err := json.Unmarshal(line, &head); err != nil {
			continue
		}
		if len(head.ID) == 0 {
			continue
		}
		switch head.Method {
		case "initialize":
			writeHelperResponse(head.ID, map[string]any{
				"protocolVersion": "2025-06-18",
				"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
				"serverInfo":      map[string]any{"name": "cli-helper", "version": "1.0"},
			})
		case "tools/list":
			writeHelperResponse(head.ID, map[string]any{
				"tools": []map[string]any{{
					"name":        "helper_echo",
					"description": "echo back the arguments",
					"inputSchema": map[string]any{"type": "object"},
				}},
			})
		case "tools/call":
			writeHelperResponse(head.ID, map[string]any{
				"content": []map[string]any{{"type": "text", "text": "helper-result"}},
				"isError": false,
			})
		default:
			writeHelperResponse(head.ID, map[string]any{
				"error": map[string]any{"code": -32601, "message": "method not found"},
			})
		}
	}
	return 0
}

func writeHelperResponse(id json.RawMessage, result map[string]any) {
	resp := map[string]any{"jsonrpc": "2.0", "id": id, "result": result}
	b, err := json.Marshal(resp)
	if err != nil {
		return
	}
	fmt.Fprintln(os.Stdout, string(b))
}
