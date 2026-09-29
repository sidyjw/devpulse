package mcp_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/sidyjw/devpulse/internal/mcp"
	"github.com/sidyjw/devpulse/internal/mcp/mcptest"
)

func TestProtocolBasics(t *testing.T) {
	s := mcp.NewServer("x", "1", "hi")
	s.AddTool(&mcp.Tool{Name: "echo", InputSchema: mcp.Obj(map[string]any{})}, func(context.Context, json.RawMessage) (any, error) {
		return map[string]any{"ok": true}, nil
	})
	res := mcptest.RPC(t, s,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"c","version":"1"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":"p","method":"ping"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":4,"method":"resources/list"}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"nope","arguments":{}}}`,
		`not json`,
	)
	var init struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	_ = json.Unmarshal(res["1"].Result, &init)
	if init.ProtocolVersion != "2025-06-18" {
		t.Errorf("versão negociada = %q", init.ProtocolVersion)
	}
	if res[`"p"`].Error != nil || string(res[`"p"`].Result) != "{}" {
		t.Errorf("ping: %+v", res[`"p"`])
	}
	if !strings.Contains(string(res["3"].Result), `"echo"`) {
		t.Errorf("tools/list: %s", res["3"].Result)
	}
	if res["4"].Error == nil || res["4"].Error.Code != -32601 {
		t.Errorf("método desconhecido deveria dar -32601: %+v", res["4"])
	}
	if res["5"].Error == nil {
		t.Errorf("tool desconhecida deveria dar erro")
	}
	if res["null"].Error == nil || res["null"].Error.Code != -32700 {
		t.Errorf("JSON inválido deveria dar parse error")
	}
	if len(res) != 6 {
		t.Errorf("notificações não devem gerar resposta; respostas=%d", len(res))
	}
}

func TestUnknownProtocolVersionFallsBackToLatest(t *testing.T) {
	s := mcp.NewServer("x", "1", "")
	res := mcptest.RPC(t, s, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2099-01-01"}}`)
	if !strings.Contains(string(res["1"].Result), "2025-11-25") {
		t.Errorf("esperava a versão mais recente: %s", res["1"].Result)
	}
}

func TestDuplicateToolPanics(t *testing.T) {
	s := mcp.NewServer("x", "1", "")
	h := func(context.Context, json.RawMessage) (any, error) { return nil, nil }
	s.AddTool(&mcp.Tool{Name: "a"}, h)
	defer func() {
		if recover() == nil {
			t.Fatal("tool duplicada deveria causar panic")
		}
	}()
	s.AddTool(&mcp.Tool{Name: "a"}, h)
}
