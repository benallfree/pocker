package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"ganigeorgiev.com/pocketbase/pocketbase/core"
)

// JSON-RPC error codes.
const (
	codeParse          = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternal       = -32603
)

const maxBody = 4 << 20

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

func rawID(id any) json.RawMessage {
	switch v := id.(type) {
	case json.RawMessage:
		if len(v) > 0 {
			return v
		}
	case nil:
	default:
		if b, err := json.Marshal(v); err == nil {
			return b
		}
	}
	return json.RawMessage("null")
}

func rpcError(id any, code int, message string) map[string]any {
	return map[string]any{"jsonrpc": "2.0", "id": rawID(id), "error": map[string]any{"code": code, "message": message}}
}

func rpcResult(id any, result any) map[string]any {
	return map[string]any{"jsonrpc": "2.0", "id": rawID(id), "result": result}
}

func (s *Server) bindRoutes(se *core.ServeEvent) {
	se.Router.POST(s.cfg.Path, s.handlePost)
	se.Router.GET(s.cfg.Path, methodNotAllowed)
	se.Router.DELETE(s.cfg.Path, methodNotAllowed)

	prm := func(e *core.RequestEvent) error {
		e.Response.Header().Set("Access-Control-Allow-Origin", "*")
		return e.JSON(http.StatusOK, s.ProtectedResourceMetadata(e))
	}
	se.Router.GET(wellKnownPRM, prm)
	se.Router.GET(wellKnownPRM+s.cfg.Path, prm)
}

func (s *Server) originAllowed(e *core.RequestEvent) bool {
	origin := e.Request.Header.Get("Origin")
	if origin == "" {
		return true // not a browser
	}
	if slices.Contains(s.cfg.AllowedOrigins, strings.TrimRight(origin, "/")) {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	if strings.EqualFold(u.Host, e.Request.Host) {
		return true
	}
	if pub, err := url.Parse(s.publicOrigin(e)); err == nil && strings.EqualFold(u.Host, pub.Host) {
		return true
	}
	return false
}

func (s *Server) handlePost(e *core.RequestEvent) error {
	if !s.originAllowed(e) {
		return e.JSON(http.StatusForbidden, rpcError(nil, codeInvalidRequest, "Origin not allowed."))
	}
	if v := e.Request.Header.Get("MCP-Protocol-Version"); v != "" && !slices.Contains(SupportedVersions, v) {
		return e.JSON(http.StatusBadRequest, rpcError(nil, codeInvalidRequest,
			"Unsupported MCP-Protocol-Version "+v+"; supported: "+strings.Join(SupportedVersions, ", ")))
	}

	grant, err := s.cfg.Auth.Authenticate(e, s.ResourceURL(e))
	if err != nil || grant == nil {
		return s.challenge(e, err)
	}

	body, err := io.ReadAll(http.MaxBytesReader(e.Response, e.Request.Body, maxBody))
	if err != nil {
		return e.JSON(http.StatusRequestEntityTooLarge, rpcError(nil, codeInvalidRequest, "Request too large."))
	}
	body = bytes.TrimSpace(body)
	if len(body) > 0 && body[0] == '[' {
		return e.JSON(http.StatusBadRequest, rpcError(nil, codeInvalidRequest, "JSON-RPC batches are not supported."))
	}
	var msg rpcRequest
	if err := json.Unmarshal(body, &msg); err != nil {
		return e.JSON(http.StatusBadRequest, rpcError(nil, codeParse, "Parse error: "+err.Error()))
	}
	if msg.JSONRPC != "2.0" {
		return e.JSON(http.StatusBadRequest, rpcError(msg.ID, codeInvalidRequest, "Invalid Request: expected a single JSON-RPC 2.0 message."))
	}
	// Notifications (no id) and the client's responses (no method) need no answer.
	if msg.Method == "" || len(msg.ID) == 0 || string(msg.ID) == "null" {
		return e.NoContent(http.StatusAccepted)
	}

	return s.dispatch(e, grant, &msg)
}

func (s *Server) dispatch(e *core.RequestEvent, grant *Grant, msg *rpcRequest) error {
	id := msg.ID
	switch msg.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(msg.Params, &p)
		version := SupportedVersions[0]
		if slices.Contains(SupportedVersions, p.ProtocolVersion) {
			version = p.ProtocolVersion
		}
		return e.JSON(http.StatusOK, rpcResult(id, map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      s.serverInfo(e),
			"instructions":    s.instructionsText(),
		}))

	case "ping":
		return e.JSON(http.StatusOK, rpcResult(id, map[string]any{}))

	case "tools/list":
		list := []map[string]any{}
		for _, t := range s.Tools() {
			if visible(t, &Call{App: s.app, Event: e, Grant: grant, Tool: t}) {
				list = append(list, toolDef(t))
			}
		}
		return e.JSON(http.StatusOK, rpcResult(id, map[string]any{"tools": list}))

	case "tools/call":
		var p struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(msg.Params, &p); err != nil {
			return e.JSON(http.StatusOK, rpcError(id, codeInvalidParams, "Invalid params: "+err.Error()))
		}
		if p.Arguments == nil {
			p.Arguments = map[string]any{}
		}
		t := s.tool(p.Name)
		c := &Call{App: s.app, Event: e, Grant: grant, Tool: t, Args: p.Arguments}
		if t == nil || t.Allow(c) != nil {
			return e.JSON(http.StatusOK, rpcError(id, codeInvalidParams, "Unknown tool: "+p.Name))
		}
		if !visible(t, c) {
			scope := ScopeWrite
			if t.Annotations.ReadOnly() {
				scope = ScopeRead
			}
			return s.insufficientScope(e, id, scope)
		}
		return e.JSON(http.StatusOK, rpcResult(id, runTool(t, c)))

	default:
		return e.JSON(http.StatusOK, rpcError(id, codeMethodNotFound, "Method not found: "+msg.Method))
	}
}

func (s *Server) serverInfo(e *core.RequestEvent) map[string]any {
	meta := s.app.Settings().Meta
	title := meta.AppName
	if title == "" {
		title = "PocketBase"
	}
	name := s.cfg.Name
	if name == "" {
		name = slug(meta.AppName)
	}
	info := map[string]any{"name": name, "title": title, "version": s.cfg.Version, "websiteUrl": s.publicOrigin(e)}
	if s.cfg.Description != "" {
		info["description"] = s.cfg.Description
	}
	if len(s.cfg.Icons) > 0 {
		info["icons"] = s.cfg.Icons
	}
	return info
}

func slug(v string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(v) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		case !dash && b.Len() > 0:
			b.WriteByte('-')
			dash = true
		}
	}
	if out := strings.Trim(b.String(), "-"); out != "" {
		return out
	}
	return "pocketbase"
}

var noArgs = map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false}

func toolDef(t *Tool) map[string]any {
	def := map[string]any{"name": t.Name, "description": t.Description}
	if t.InputSchema != nil {
		def["inputSchema"] = t.InputSchema
	} else {
		def["inputSchema"] = noArgs
	}
	if t.Title != "" {
		def["title"] = t.Title
	}
	if t.OutputSchema != nil {
		def["outputSchema"] = t.OutputSchema
	}
	if t.Annotations != nil {
		def["annotations"] = t.Annotations
	}
	return def
}

// runTool calls the handler; failures (errors and panics) become isError results the model can read.
func runTool(t *Tool, c *Call) (result map[string]any) {
	defer func() {
		if r := recover(); r != nil {
			result = errorResult(fmt.Errorf("%v", r))
		}
	}()
	v, err := t.Handler(c)
	if err != nil {
		return errorResult(err)
	}
	return toolResult(v)
}

func errorResult(err error) map[string]any {
	return map[string]any{"isError": true, "content": []map[string]any{{"type": "text", "text": err.Error()}}}
}

func toolResult(v any) map[string]any {
	if s, ok := v.(string); ok {
		return map[string]any{"content": []map[string]any{{"type": "text", "text": s}}}
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return errorResult(fmt.Errorf("result is not JSON: %w", err))
	}
	var pretty bytes.Buffer
	if json.Indent(&pretty, raw, "", "  ") != nil {
		pretty.Write(raw)
	}
	out := map[string]any{"content": []map[string]any{{"type": "text", "text": pretty.String()}}}
	if len(raw) > 0 && raw[0] == '{' {
		out["structuredContent"] = json.RawMessage(raw)
	}
	return out
}
