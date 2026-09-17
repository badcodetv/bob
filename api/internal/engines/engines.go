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
	}
	return kind, false
}
