// Package mcptest holds helpers shared by the tests of every provider: a
// recording fake HTTP server and functions that drive an mcp.Server.
package mcptest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/sidyjw/devpulse/internal/mcp"
)

// Recorded is one request received by a Fake.
type Recorded struct {
	Method, Path, RawQuery, Auth, ContentType string
	Body                                      []byte
}

// Fake is an http.Handler that records every request and delegates to H.
type Fake struct {
	mu   sync.Mutex
	Reqs []Recorded
	H    func(w http.ResponseWriter, r *http.Request, body []byte)
}

func (f *Fake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.Reqs = append(f.Reqs, Recorded{r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Get("Authorization"), r.Header.Get("Content-Type"), b})
	f.mu.Unlock()
	f.H(w, r, b)
}

// Find returns the recorded requests with this method whose path ends in pathSuffix.
func (f *Fake) Find(method, pathSuffix string) []Recorded {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []Recorded
	for _, r := range f.Reqs {
		if r.Method == method && strings.HasSuffix(r.Path, pathSuffix) {
			out = append(out, r)
		}
	}
	return out
}

// All returns a copy of every recorded request.
func (f *Fake) All() []Recorded {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Recorded(nil), f.Reqs...)
}

func WriteJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type RPCResp struct {
	ID     json.RawMessage `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *RPCError       `json:"error"`
}

// RPC feeds newline-delimited messages to the server and returns responses by id.
func RPC(t *testing.T, s *mcp.Server, msgs ...string) map[string]RPCResp {
	t.Helper()
	var out bytes.Buffer
	if err := s.Serve(context.Background(), strings.NewReader(strings.Join(msgs, "\n")+"\n"), &out); err != nil {
		t.Fatal(err)
	}
	res := map[string]RPCResp{}
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if line == "" {
			continue
		}
		var r RPCResp
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("resposta inválida %q: %v", line, err)
		}
		res[string(r.ID)] = r
	}
	return res
}

// ListTools returns the raw tools/list result.
func ListTools(t *testing.T, s *mcp.Server) string {
	t.Helper()
	return string(RPC(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)["1"].Result)
}

type ToolResult struct {
	Content []struct {
		Text string `json:"text"`
	} `json:"content"`
	IsError           bool            `json:"isError"`
	StructuredContent json.RawMessage `json:"structuredContent"`
}

func CallTool(t *testing.T, s *mcp.Server, name string, args any) ToolResult {
	t.Helper()
	a, _ := json.Marshal(args)
	msg := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":%q,"arguments":%s}}`, name, a)
	r := RPC(t, s, msg)["1"]
	if r.Error != nil {
		t.Fatalf("rpc error: %+v", r.Error)
	}
	var tr ToolResult
	if err := json.Unmarshal(r.Result, &tr); err != nil {
		t.Fatal(err)
	}
	return tr
}

func (tr ToolResult) Text() string {
	if len(tr.Content) == 0 {
		return ""
	}
	return tr.Content[0].Text
}
