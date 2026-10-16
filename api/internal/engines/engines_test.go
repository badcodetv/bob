package engines

import "testing"

func TestClassify(t *testing.T) {
	cases := []struct {
		engine, event, kind string
		ephemeral           bool
	}{
		{"claude", `{"type":"system","subtype":"init"}`, "system.init", false},
		{"claude", `{"type":"assistant","message":{}}`, "assistant", false},
		{"claude", `{"type":"stream_event","event":{}}`, "stream_event", true},
		{"codex", `{"type":"stream_event"}`, "stream_event", false},
		{"claude", `not json`, "unknown", false},
		{"codex", `{"method":"thread/started","params":{}}`, "thread/started", false},
		{"codex", `{"method":"item/started","params":{}}`, "item/started", true},
		{"codex", `{"method":"item/agentMessage/delta","params":{}}`, "item/agentMessage/delta", true},
		{"codex", `{"method":"thread/tokenUsage/updated","params":{}}`, "thread/tokenUsage/updated", true},
		{"codex", `{"method":"item/completed","params":{}}`, "item/completed", false},
		{"codex", `{"method":"turn/completed","params":{}}`, "turn/completed", false},
		{"codex", `{"method":"error","params":{}}`, "error", false},
	}
	for _, c := range cases {
		kind, eph := Classify(c.engine, []byte(c.event))
		if kind != c.kind || eph != c.ephemeral {
			t.Errorf("Classify(%s, %s) = %q, %v; want %q, %v", c.engine, c.event, kind, eph, c.kind, c.ephemeral)
		}
	}
}

func TestHarnessSessionID(t *testing.T) {
	cases := []struct{ engine, event, want string }{
		{"claude", `{"type":"system","subtype":"init","session_id":"abc"}`, "abc"},
		{"claude", `{"type":"assistant","session_id":"abc"}`, ""},
		{"codex", `{"method":"thread/started","params":{"thread":{"id":"t1"}}}`, "t1"},
		{"codex", `{"method":"turn/started","params":{"thread":{"id":"t1"}}}`, ""},
		{"codex", `{"type":"system","subtype":"init","session_id":"abc"}`, ""},
		{"claude", `not json`, ""},
	}
	for _, c := range cases {
		if got := HarnessSessionID(c.engine, []byte(c.event)); got != c.want {
			t.Errorf("HarnessSessionID(%s, %s) = %q; want %q", c.engine, c.event, got, c.want)
		}
	}
}
