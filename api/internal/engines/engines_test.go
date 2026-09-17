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
	}
	for _, c := range cases {
		kind, eph := Classify(c.engine, []byte(c.event))
		if kind != c.kind || eph != c.ephemeral {
			t.Errorf("Classify(%s, %s) = %q, %v; want %q, %v", c.engine, c.event, kind, eph, c.kind, c.ephemeral)
		}
	}
}
