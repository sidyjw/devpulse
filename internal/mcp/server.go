// Package mcp is a deliberately small Model Context Protocol server: JSON-RPC
// 2.0 over newline-delimited stdio, supporting initialize, ping and tools.
// Written against the standard library only so that the whole attack surface
// is the code in this repository.
//
// Spec: https://modelcontextprotocol.io/specification
package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
)

// LogPrefix is prepended to every log line written to stderr.
var LogPrefix = "[pm-mcp] "

// Logf writes a log line to stderr (stdout carries the protocol).
func Logf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, LogPrefix+format+"\n", args...)
}

var supportedProtocolVersions = []string{"2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05"}

const maxMessageBytes = 4 << 20

type ToolAnnotations struct {
	Title           string `json:"title,omitempty"`
	ReadOnlyHint    bool   `json:"readOnlyHint"`
	DestructiveHint bool   `json:"destructiveHint"`
	IdempotentHint  bool   `json:"idempotentHint"`
	OpenWorldHint   bool   `json:"openWorldHint"`
}

type ToolHandler func(ctx context.Context, args json.RawMessage) (any, error)

type Tool struct {
	Name        string           `json:"name"`
	Title       string           `json:"title,omitempty"`
	Description string           `json:"description"`
	InputSchema map[string]any   `json:"inputSchema"`
	Annotations *ToolAnnotations `json:"annotations,omitempty"`
	handler     ToolHandler
}

type Server struct {
	name, version, instructions string
	tools                       []*Tool
	byName                      map[string]*Tool

	out   io.Writer
	outMu sync.Mutex

	inflightMu sync.Mutex
	inflight   map[string]context.CancelFunc
	wg         sync.WaitGroup
}

func NewServer(name, version, instructions string) *Server {
	return &Server{
		name: name, version: version, instructions: instructions,
		byName:   map[string]*Tool{},
		inflight: map[string]context.CancelFunc{},
	}
}

// AddTool registers a tool. Tool names are global across every provider, so
// a duplicate is a programming error and panics at startup; new providers
// should prefix their tool names (e.g. "jira_…").
func (s *Server) AddTool(t *Tool, h ToolHandler) {
	if _, dup := s.byName[t.Name]; dup {
		panic(fmt.Sprintf("mcp: tool %q registrada duas vezes", t.Name))
	}
	t.handler = h
	s.tools = append(s.tools, t)
	s.byName[t.Name] = t
}

// ToolNames lists the registered tools in registration order.
func (s *Server) ToolNames() []string {
	names := make([]string, len(s.tools))
	for i, t := range s.tools {
		names[i] = t.Name
	}
	return names
}

// ---------- JSON-RPC ----------

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

const (
	codeParseError     = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
)

// Serve reads requests from r and writes responses to w until r is closed.
func (s *Server) Serve(ctx context.Context, r io.Reader, w io.Writer) error {
	s.out = w
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), maxMessageBytes)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		msg := make([]byte, len(line))
		copy(msg, line)

		if msg[0] == '[' { // legacy JSON-RPC batch (protocol 2025-03-26)
			var batch []json.RawMessage
			if err := json.Unmarshal(msg, &batch); err != nil {
				s.send(rpcResponse{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{codeParseError, "parse error"}})
				continue
			}
			for _, m := range batch {
				s.dispatch(ctx, m)
			}
			continue
		}
		s.dispatch(ctx, msg)
	}
	s.wg.Wait()
	if err := sc.Err(); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func (s *Server) dispatch(ctx context.Context, msg []byte) {
	var req rpcRequest
	if err := json.Unmarshal(msg, &req); err != nil {
		s.send(rpcResponse{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{codeParseError, "parse error"}})
		return
	}
	isNotification := len(req.ID) == 0 || string(req.ID) == "null"
	if req.Method == "" {
		// A response from the client (we never send requests) — ignore.
		return
	}
	if isNotification {
		s.handleNotification(req)
		return
	}

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		rctx, cancel := context.WithCancel(ctx)
		key := string(req.ID)
		s.inflightMu.Lock()
		s.inflight[key] = cancel
		s.inflightMu.Unlock()
		defer func() {
			s.inflightMu.Lock()
			delete(s.inflight, key)
			s.inflightMu.Unlock()
			cancel()
		}()

		result, rerr := s.handleRequest(rctx, req)
		if rctx.Err() != nil && ctx.Err() == nil {
			return // cancelled by the client: spec says do not respond
		}
		resp := rpcResponse{JSONRPC: "2.0", ID: req.ID}
		if rerr != nil {
			resp.Error = rerr
		} else {
			resp.Result = result
		}
		s.send(resp)
	}()
}

func (s *Server) handleNotification(req rpcRequest) {
	switch req.Method {
	case "notifications/cancelled":
		var p struct {
			RequestID json.RawMessage `json:"requestId"`
		}
		if json.Unmarshal(req.Params, &p) == nil {
			s.inflightMu.Lock()
			if c, ok := s.inflight[string(p.RequestID)]; ok {
				c()
			}
			s.inflightMu.Unlock()
		}
	default:
		// notifications/initialized and anything else: nothing to do.
	}
}

func (s *Server) handleRequest(ctx context.Context, req rpcRequest) (any, *rpcError) {
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p)
		ver := supportedProtocolVersions[0]
		for _, v := range supportedProtocolVersions {
			if v == p.ProtocolVersion {
				ver = v
			}
		}
		return map[string]any{
			"protocolVersion": ver,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": s.name, "version": s.version},
			"instructions":    s.instructions,
		}, nil

	case "ping":
		return map[string]any{}, nil

	case "tools/list":
		return map[string]any{"tools": s.tools}, nil

	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &rpcError{codeInvalidParams, "invalid params"}
		}
		t, ok := s.byName[p.Name]
		if !ok {
			return nil, &rpcError{codeInvalidParams, "unknown tool: " + p.Name}
		}
		args := p.Arguments
		if len(args) == 0 || string(args) == "null" {
			args = json.RawMessage("{}")
		}
		return s.callTool(ctx, t, args), nil

	default:
		return nil, &rpcError{codeMethodNotFound, "method not found: " + req.Method}
	}
}

func (s *Server) callTool(ctx context.Context, t *Tool, args json.RawMessage) (res map[string]any) {
	defer func() {
		if r := recover(); r != nil {
			Logf("panic em %s: %v", t.Name, r)
			res = toolError(fmt.Errorf("erro interno em %s", t.Name))
		}
	}()
	out, err := t.handler(ctx, args)
	if err != nil {
		return toolError(err)
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return toolError(err)
	}
	res = map[string]any{
		"content": []map[string]any{{"type": "text", "text": string(b)}},
		"isError": false,
	}
	if len(b) > 0 && b[0] == '{' {
		res["structuredContent"] = json.RawMessage(b)
	}
	return res
}

func toolError(err error) map[string]any {
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": "Erro: " + err.Error()}},
		"isError": true,
	}
}

func (s *Server) send(r rpcResponse) {
	b, err := json.Marshal(r)
	if err != nil {
		Logf("falha ao serializar resposta: %v", err)
		return
	}
	s.outMu.Lock()
	defer s.outMu.Unlock()
	b = append(b, '\n')
	if _, err := s.out.Write(b); err != nil {
		Logf("falha ao escrever no stdout: %v", err)
	}
}

// DecodeArgs unmarshals tool arguments strictly: unknown fields are an
// error, which surfaces model mistakes instead of silently ignoring them.
func DecodeArgs(raw json.RawMessage, v any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("argumentos inválidos: %v", err)
	}
	return nil
}
