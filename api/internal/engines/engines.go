// Package engines holds the few facts Bob needs about each harness's native events.
// There is deliberately no common event type: code that reads events switches on engine.
package engines

import "encoding/json"

// codexStored are the Codex app-server notifications Bob keeps. Everything else it sends — token
// deltas, items starting, token counts, MCP startup, rate limits — is streamed live and dropped:
// item/completed carries each item's final state, and turn/completed how the turn ended.
var codexStored = map[string]bool{
	"thread/started": true, "turn/started": true, "item/completed": true, "turn/completed": true,
	"error": true, "warning": true, "model/rerouted": true,
}

// Classify returns the stored kind of a native event, and whether it is ephemeral
// (streamed live to watchers but never written to Postgres, e.g. token deltas).
func Classify(engine string, event json.RawMessage) (kind string, ephemeral bool) {
	var head struct {
		Type    string `json:"type"`
		Subtype string `json:"subtype"`
		Method  string `json:"method"`
	}
	_ = json.Unmarshal(event, &head)
	if engine == "codex" && head.Method != "" {
		// A JSON-RPC notification from `codex app-server`: its method is its kind.
		return head.Method, !codexStored[head.Method]
	}
	kind = head.Type
	if head.Subtype != "" {
		kind += "." + head.Subtype
	}
	if kind == "" {
		kind = "unknown"
	}
	switch engine {
	case "claude":
		// stream_event carries partial-message deltas; the complete message follows.
		return kind, head.Type == "stream_event"
	}
	return kind, false
}

// HarnessSessionID returns the harness's own session id if this event announces it, else "".
// Claude's system.init carries it as session_id, Codex's thread/started as params.thread.id. Bob
// saves it as soon as it is seen, so a turn that is cut off still leaves the chat resumable.
func HarnessSessionID(engine string, event json.RawMessage) string {
	var head struct {
		Type      string `json:"type"`
		Subtype   string `json:"subtype"`
		SessionID string `json:"session_id"`
		Method    string `json:"method"`
		Params    struct {
			Thread struct {
				ID string `json:"id"`
			} `json:"thread"`
		} `json:"params"`
	}
	if json.Unmarshal(event, &head) != nil {
		return ""
	}
	switch {
	case engine == "claude" && head.Type == "system" && head.Subtype == "init":
		return head.SessionID
	case engine == "codex" && head.Method == "thread/started":
		return head.Params.Thread.ID
	}
	return ""
}
