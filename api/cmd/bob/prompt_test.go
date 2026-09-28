package main

import "testing"

// The Bob environment note is exact text (Architecture → "Bob environment note"), with the
// project name filled in.
func TestBobNote(t *testing.T) {
	want := `You are working inside Bob, BadCode's agent runner, in the project "wolf".
- Your working folder is /project/work. Every chat in this project shares it, so other chats may
  be changing files there at the same time. Clone repositories one level below it
  (/project/work/<repo>).
- Skills live in /project/skills. You may add or edit them; every chat in the project sees them.
- Bob's own tools are on the MCP server named "bob": workers, schedules, memory, Google Drive
  (when connected) and request_human_attention.`
	if got := bobNote("wolf"); got != want {
		t.Errorf("bobNote(%q) =\n%s\nwant\n%s", "wolf", got, want)
	}
}

// composePrompt joins its non-empty parts with a blank line, in order, skipping empty ones.
func TestComposePrompt(t *testing.T) {
	cases := []struct {
		parts []string
		want  string
	}{
		{[]string{"a", "b", "c"}, "a\n\nb\n\nc"},
		{[]string{"a", "", "c"}, "a\n\nc"},
		{[]string{"", "", ""}, ""},
		{[]string{"only"}, "only"},
		{[]string{}, ""},
		{[]string{"", "b"}, "b"},
	}
	for _, c := range cases {
		if got := composePrompt(c.parts...); got != c.want {
			t.Errorf("composePrompt(%q) = %q, want %q", c.parts, got, c.want)
		}
	}
}
