// Package engines holds the few facts Bob needs about each harness's native events.
// There is deliberately no common event type: code that reads events switches on engine.
package engines

import "encoding/json"

// Classify returns the stored kind of a native event, and whether it is ephemeral
// (streamed live to watchers but never written to Postgres, e.g. token deltas).
func Classify(engine string, event json.RawMessage) (kind string, ephemeral bool) {
	var head struct {
		Type    string `json:"type"`
		Subtype string `json:"subtype"`
	}
	_ = json.Unmarshal(event, &head)
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
	case "codex":
		// item.started/item.updated are in-progress snapshots of an item (e.g. a command still
		// running); item.completed carries the final state and is what gets stored.
		return kind, head.Type == "item.started" || head.Type == "item.updated"
	}
	return kind, false
}

// HarnessSessionID returns the harness's own session id if this event announces it, else "".
// Claude's system.init carries it as session_id, Codex's thread.started as thread_id. Bob saves
// it as soon as it is seen, so a turn that is cut off still leaves the chat resumable.
func HarnessSessionID(engine string, event json.RawMessage) string {
	var head struct {
		Type      string `json:"type"`
		Subtype   string `json:"subtype"`
		SessionID string `json:"session_id"`
		ThreadID  string `json:"thread_id"`
	}
	if json.Unmarshal(event, &head) != nil {
		return ""
	}
	switch {
	case engine == "claude" && head.Type == "system" && head.Subtype == "init":
		return head.SessionID
	case engine == "codex" && head.Type == "thread.started":
		return head.ThreadID
	}
	return ""
}
