package pluginkit

import "ganigeorgiev.com/pocketbase/pocketbase/core"

// ToolInfo describes one MCP tool on an app: what clients see in tools/list.
type ToolInfo struct {
	Name        string
	Description string
	// InputSchema is the tool's JSON Schema for its arguments (nil: no arguments).
	InputSchema map[string]any
}

// ToolLister lists an app's MCP tools. The MCP server on an app (ext/mcp) records itself with [SetToolLister], so
// checks such as the conformance test can see which tools a plugin added without importing it.
type ToolLister interface {
	ToolInfos() []ToolInfo
}

const toolListerKey = "pluginkit.mcpTools"

// SetToolLister records app's MCP server. ext/mcp calls it when it's registered on app.
func SetToolLister(app core.App, l ToolLister) { app.Store().Set(toolListerKey, l) }

// Tools lists app's MCP tools; ok is false when app has no MCP server.
func Tools(app core.App) (tools []ToolInfo, ok bool) {
	l, ok := app.Store().Get(toolListerKey).(ToolLister)
	if !ok {
		return nil, false
	}
	return l.ToolInfos(), true
}
