// Package mcp is Bob's MCP server: the tools an agent calls back into Bob with, served by the API
// as JSON-RPC 2.0 over streamable HTTP (one request per POST, one plain JSON response; no SSE).
//
// Who calls is decided by the auth func the API passes to Handler, from the chat's bearer token,
// never from a tool argument: no tool takes a project, the token is the scope.
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

const (
	protocolVersion = "2025-06-18"
	maxRequestBytes = 8 << 20 // one request is read into memory before it is parsed
)

// JSON-RPC 2.0 error codes.
const (
	parseError     = -32700
	invalidRequest = -32600
	methodNotFound = -32601
	invalidParams  = -32602
	internalError  = -32603
)

// Caller is the chat calling a tool.
type Caller struct {
	Project, SessionID string
	Worker             string // the session's worker name; '' for a plain chat
	User               string // email of the session's latest bob.user_message, or "schedule:<id>"
	APIBase            string // "http://" + the /mcp request's Host header: how this container reaches the API
}

// Tool is one tool. A Call error is reported to the model as an isError result: a tool called
// wrongly is something the model can fix on its next turn.
type Tool struct {
	Name, Description string
	InputSchema       json.RawMessage   // nil = no arguments
	Available         func(Caller) bool // nil = always
	Call              func(ctx context.Context, c Caller, args json.RawMessage) (any, error)
}

func (t *Tool) availableTo(c Caller) bool { return t.Available == nil || t.Available(c) }

// Errors an auth func returns: ErrUnauthorized → 401, ErrNoSession → 404, anything else → 500.
var (
	ErrUnauthorized = errors.New("missing or bad MCP token")
	ErrNoSession    = errors.New("this chat no longer exists")
)

type Server struct {
	tools map[string]*Tool
	order []string // registration order, so tools/list is stable
}

func New() *Server { return &Server{tools: map[string]*Tool{}} }

// Register adds tools. A tool without a name or Call, or a name registered twice, panics: a
// programming error seen the first time the API starts.
func (s *Server) Register(t ...Tool) {
	for _, tool := range t {
		if tool.Name == "" || tool.Call == nil {
			panic("mcp: a tool needs a name and a Call")
		}
		if _, dup := s.tools[tool.Name]; dup {
			panic(fmt.Sprintf("mcp: tool %q registered twice", tool.Name))
		}
		s.tools[tool.Name] = &tool
		s.order = append(s.order, tool.Name)
	}
}

// Handler serves /mcp. Bob never starts anything towards a chat, so there is no server→client
// stream: GET is refused with 405, which tells a client to carry on with POSTs alone.
func (s *Server) Handler(auth func(r *http.Request) (Caller, error)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		caller, err := auth(r)
		switch {
		case errors.Is(err, ErrUnauthorized):
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		case errors.Is(err, ErrNoSession):
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		case err != nil:
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		body, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBytes+1))
		if err != nil {
			writeRPC(w, nil, nil, &rpcError{parseError, "reading the request: " + err.Error()})
			return
		}
		if len(body) > maxRequestBytes {
			writeRPC(w, nil, nil, &rpcError{invalidRequest, fmt.Sprintf("request is over %d bytes", maxRequestBytes)})
			return
		}
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			writeRPC(w, nil, nil, &rpcError{parseError, "parse error: " + err.Error()})
			return
		}
		// No id: a notification (notifications/initialized is the one a tools-only server sees).
		if len(req.ID) == 0 || string(req.ID) == "null" {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		result, rpcErr := s.dispatch(r.Context(), caller, req.Method, req.Params)
		writeRPC(w, req.ID, result, rpcErr)
	})
}

func (s *Server) dispatch(ctx context.Context, c Caller, method string, params json.RawMessage) (any, *rpcError) {
	switch method {
	case "initialize":
		return map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "bob", "version": "1"},
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		tools := []map[string]any{}
		for _, name := range s.order {
			t := s.tools[name]
			if !t.availableTo(c) {
				continue
			}
			schema := t.InputSchema
			if schema == nil {
				schema = json.RawMessage(`{"type":"object","properties":{}}`)
			}
			tools = append(tools, map[string]any{"name": t.Name, "description": t.Description, "inputSchema": schema})
		}
		return map[string]any{"tools": tools}, nil
	case "tools/call":
		return s.call(ctx, c, params)
	}
	return nil, &rpcError{methodNotFound, "unknown method " + method}
}

// call runs a tool. Naming a tool the caller does not have is a client error (-32602); a tool that
// ran and refused is information for the model, so it comes back as a result flagged isError.
func (s *Server) call(ctx context.Context, c Caller, params json.RawMessage) (any, *rpcError) {
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &rpcError{invalidParams, "invalid params: " + err.Error()}
	}
	t, ok := s.tools[p.Name]
	if !ok || !t.availableTo(c) {
		return nil, &rpcError{invalidParams, fmt.Sprintf("no tool named %q", p.Name)}
	}
	result, err := t.Call(ctx, c, p.Arguments)
	if err != nil {
		return map[string]any{"isError": true, "content": []map[string]string{{"type": "text", "text": err.Error()}}}, nil
	}
	text, err := json.Marshal(result)
	if err != nil {
		return nil, &rpcError{internalError, "encoding the result: " + err.Error()}
	}
	return map[string]any{"content": []map[string]string{{"type": "text", "text": string(text)}}}, nil
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// writeRPC writes a JSON-RPC response. Errors ride a 200 too: the error is in the envelope. An
// error with no request id (the request could not be read) answers id null, as JSON-RPC requires.
func writeRPC(w http.ResponseWriter, id json.RawMessage, result any, rpcErr *rpcError) {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	out := map[string]any{"jsonrpc": "2.0", "id": id}
	if rpcErr != nil {
		out["error"] = rpcErr
	} else {
		out["result"] = result
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}
