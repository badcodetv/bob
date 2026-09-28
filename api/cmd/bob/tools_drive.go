package main

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/badcodetv/bob/internal/mcp"
)

// driveFetchTTL is how long a drive_fetch URL stays valid: long enough for an agent to hand it to
// curl in the same turn, short enough that a leaked link is useless soon after.
const driveFetchTTL = 10 * time.Minute

// driveTools returns drive_search, drive_list, drive_read and drive_fetch, available only to a
// project that has a Drive client (main.go's driveClientsFromEnv, keyed on a.drive). A project
// with no BOB_DRIVE_TOKEN_<NAME> set never sees these tools in tools/list.
func (a *app) driveTools() []mcp.Tool {
	available := func(c mcp.Caller) bool {
		_, ok := a.drive[c.Project]
		return ok
	}
	return []mcp.Tool{
		{
			Name:        "drive_search",
			Description: "Full-text searches this project's Google Drive, most recently modified first.",
			InputSchema: driveSearchSchema,
			Available:   available,
			Call: func(ctx context.Context, c mcp.Caller, args json.RawMessage) (any, error) {
				cl, ok := a.drive[c.Project]
				if !ok {
					return nil, errNoDrive
				}
				var in struct {
					Query string `json:"query"`
					Limit int    `json:"limit"`
				}
				if err := json.Unmarshal(args, &in); err != nil {
					return nil, err
				}
				if in.Query == "" {
					return nil, errors.New("query is required")
				}
				limit := in.Limit
				if limit <= 0 {
					limit = 20
				}
				if limit > 50 {
					limit = 50
				}
				files, err := cl.Search(ctx, in.Query, limit)
				if err != nil {
					return nil, err
				}
				return files, nil
			},
		},
		{
			Name: "drive_list",
			Description: "Lists one page of this project's Google Drive: the children of folder_id, or, when " +
				"folder_id is left out, My Drive's root and everything shared with the caller.",
			InputSchema: driveListSchema,
			Available:   available,
			Call: func(ctx context.Context, c mcp.Caller, args json.RawMessage) (any, error) {
				cl, ok := a.drive[c.Project]
				if !ok {
					return nil, errNoDrive
				}
				var in struct {
					FolderID  string `json:"folder_id"`
					PageToken string `json:"page_token"`
				}
				if len(args) > 0 {
					if err := json.Unmarshal(args, &in); err != nil {
						return nil, err
					}
				}
				files, next, err := cl.List(ctx, in.FolderID, in.PageToken)
				if err != nil {
					return nil, err
				}
				return map[string]any{"files": files, "next_page_token": next}, nil
			},
		},
		{
			Name: "drive_read",
			Description: "Reads a Drive file as text: Google Docs export as markdown, Sheets as CSV (first " +
				"sheet only), Slides as plain text, and any text/* or application/json file as its own bytes. " +
				"Anything else errors, telling you to use drive_fetch instead. offset/limit page through the " +
				"text in characters, for files too large for one call.",
			InputSchema: driveReadSchema,
			Available:   available,
			Call: func(ctx context.Context, c mcp.Caller, args json.RawMessage) (any, error) {
				cl, ok := a.drive[c.Project]
				if !ok {
					return nil, errNoDrive
				}
				var in struct {
					FileID string `json:"file_id"`
					Offset int    `json:"offset"`
					Limit  int    `json:"limit"`
				}
				if err := json.Unmarshal(args, &in); err != nil {
					return nil, err
				}
				if in.FileID == "" {
					return nil, errors.New("file_id is required")
				}
				name, mime, text, err := cl.Read(ctx, in.FileID)
				if err != nil {
					return nil, err
				}
				limit := in.Limit
				if limit <= 0 {
					limit = 50000
				}
				offset := in.Offset
				if offset < 0 {
					offset = 0
				}
				runes := []rune(text)
				total := len(runes)
				if offset > total {
					offset = total
				}
				end := offset + limit
				if end > total {
					end = total
				}
				return map[string]any{
					"name": name, "mime_type": mime, "text": string(runes[offset:end]),
					"offset": offset, "total_chars": total,
				}, nil
			},
		},
		{
			Name: "drive_fetch",
			Description: "Mints a short-lived signed URL to download a Drive file's raw bytes (for files " +
				"drive_read can't show as text). Use curl -fsSL \"$url\" -o <file> — the URL expires in 10 minutes.",
			InputSchema: driveFetchSchema,
			Available:   available,
			Call: func(ctx context.Context, c mcp.Caller, args json.RawMessage) (any, error) {
				cl, ok := a.drive[c.Project]
				if !ok {
					return nil, errNoDrive
				}
				var in struct {
					FileID string `json:"file_id"`
				}
				if err := json.Unmarshal(args, &in); err != nil {
					return nil, err
				}
				if in.FileID == "" {
					return nil, errors.New("file_id is required")
				}
				rc, name, mime, err := cl.Download(ctx, in.FileID)
				if err != nil {
					return nil, err
				}
				_ = rc.Close()
				exp := time.Now().Add(driveFetchTTL)
				token := signDriveFetchToken(a.auth.Secret, c.Project, in.FileID, exp)
				return map[string]any{
					"url": c.APIBase + "/drive/fetch/" + token, "name": name, "mime_type": mime,
					"expires_at": exp,
				}, nil
			},
		},
	}
}

var errNoDrive = errors.New("no Drive connection for this project")

var driveSearchSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"query": {"type": "string"},
		"limit": {"type": "integer", "description": "max 50, default 20"}
	},
	"required": ["query"]
}`)

var driveListSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"folder_id": {"type": "string", "description": "default: My Drive root and Shared with me"},
		"page_token": {"type": "string"}
	}
}`)

var driveReadSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"file_id": {"type": "string"},
		"offset": {"type": "integer", "description": "chars, default 0"},
		"limit": {"type": "integer", "description": "chars, default 50000"}
	},
	"required": ["file_id"]
}`)

var driveFetchSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"file_id": {"type": "string"}
	},
	"required": ["file_id"]
}`)
