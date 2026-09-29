package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/badcodetv/bob/internal/mcp"
)

// attentionTools returns request_human_attention. Adapted from agent-bob's tool of the same
// name, without expiry: a request waits until a person replies in the chat or dismisses it.
func (a *app) attentionTools() []mcp.Tool {
	return []mcp.Tool{{
		Name:        "request_human_attention",
		Description: requestHumanAttentionDescription,
		InputSchema: requestHumanAttentionSchema,
		Call:        a.requestHumanAttention,
	}}
}

const requestHumanAttentionDescription = `Tell a human that this thread needs them, then END YOUR TURN.

Use it when you need permission, a judgement call, or a fact only a person has. ` +
	`Say specifically what you need in the message — the human sees your message ` +
	`and a link to this conversation.

There is no approval queue and no waiting state. Your session simply pauses the ` +
	`way every session pauses. The human opens the link, reads the thread, and ` +
	`WHATEVER THEY TYPE IS YOUR NEXT MESSAGE: "post it" is permission, "change the ` +
	`tone" is a conversation. So do not ask a yes/no question you cannot act on ` +
	`from either answer, and do not keep working after calling this.

The request stays open until a person replies in this chat or dismisses it; it does not expire.

notice (boolean, optional) says this is NOT a question: you have already done ` +
	`the thing and are telling a human what you did. Use it for act-then-notify ` +
	`reports, and leave it off whenever you actually need an answer.

If the project has no attention webhook configured, the request is still recorded ` +
	`and shown in Bob; the thread is the review surface either way.`

var requestHumanAttentionSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"message": {"type": "string", "description": "What you need from the person, specifically."},
		"notice": {"type": "boolean", "description": "True when this is a report of something already done, not a question."}
	},
	"required": ["message"]
}`)

func (a *app) requestHumanAttention(ctx context.Context, c mcp.Caller, args json.RawMessage) (any, error) {
	var in struct {
		Message string `json:"message"`
		Notice  bool   `json:"notice"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.Message) == "" {
		return nil, errors.New("message is required and must not be blank")
	}
	kind := "ask"
	if in.Notice {
		kind = "notice"
	}
	x, err := a.store.CreateAttention(ctx, c.SessionID, c.Worker, in.Message, kind)
	if err != nil {
		return nil, err
	}
	a.notifyAttention(x)
	return map[string]string{"id": x.ID, "chat_url": a.chatURL(x.Project, x.SessionID),
		"note": "Recorded. End your turn now: whatever the person types in this chat is your next message."}, nil
}
