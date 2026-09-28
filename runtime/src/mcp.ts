// Bob's own MCP server, as the Agent SDK's http config: the API's /mcp, authenticated with this
// chat's per-turn bearer token (Decision 5). Every turn gets its own: an agent can only act as the
// chat it is running in.
export function bobMcp(apiUrl: string, mcpToken: string): { url: string; headers: Record<string, string> } {
  return { url: `${apiUrl}/mcp`, headers: { Authorization: `Bearer ${mcpToken}` } };
}
