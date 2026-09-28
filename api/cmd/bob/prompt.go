package main

import "strings"

// bobNote is the first part of every turn's system prompt (Architecture → "Bob environment
// note"), naming the project.
func bobNote(project string) string {
	return `You are working inside Bob, BadCode's agent runner, in the project "` + project + `".
- Your working folder is /project/work. Every chat in this project shares it, so other chats may
  be changing files there at the same time. Clone repositories one level below it
  (/project/work/<repo>).
- Skills live in /project/skills. You may add or edit them; every chat in the project sees them.
- Bob's own tools are on the MCP server named "bob": workers, schedules, memory, Google Drive
  (when connected) and request_human_attention.`
}

// composePrompt joins its non-empty parts with a blank line, skipping empty ones: the Bob note,
// the project prompt and the worker prompt.
func composePrompt(parts ...string) string {
	var kept []string
	for _, p := range parts {
		if p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, "\n\n")
}
