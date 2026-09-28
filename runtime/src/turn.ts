// A turn is everything the runtime needs to run one message: Bob's API reads the worker and the
// project from its database, composes the system prompt, and sends it all in POST /turns. The
// runtime keeps no config of its own, so a change made in Bob takes effect on the next message.

/** POST /turns, as Bob's API sends it (Go mirror: api/internal/runtime.TurnRequest). */
export interface TurnRequest {
  session_id: string;
  engine: 'claude' | 'codex';
  model?: string;
  effort?: string;
  /** Claude only (allowedTools); ignored by codex. */
  tools?: string[];
  /** Composed by the API; appended to the harness's own system prompt. */
  system_prompt: string;
  /** The chat's bearer token for Bob's MCP server; '' until Bob sends one (a missing one reads as ''). */
  mcp_token: string;
  text: string;
  resume?: string;
  user_email?: string;
  user_name?: string;
}

export interface Turn {
  sessionId: string;
  engine: string;
  text: string;
  /** The harness's own session id from the previous turn; absent on the first turn. */
  resume?: string;
  /** The model and effort to run with, already resolved by the API (a chat's override, else its worker's). */
  model?: string;
  effort?: string;
  /** Tools the agent may use without asking. Absent = the harness default. */
  tools?: string[];
  systemPrompt: string;
  mcpToken: string;
  /** Working directory: the project's one shared work folder. */
  cwd: string;
  /** Who the turn runs for: a signed-in email, or "schedule:<id>" for a scheduled turn. */
  userEmail?: string;
  userName?: string;
}

/**
 * Checks a POST /turns body and turns it into a Turn run in cwd. engines are the drivers this
 * runtime has; a turn for any other is refused rather than run on the wrong harness.
 */
export function parseTurn(body: unknown, engines: readonly string[], cwd: string): { turn: Turn } | { error: string } {
  if (typeof body !== 'object' || body === null || Array.isArray(body)) return { error: 'the body must be a JSON object' }
  const b = body as Record<string, unknown>
  if (typeof b.session_id !== 'string' || !/^[\w-]+$/.test(b.session_id)) return { error: 'session_id is required: letters, digits, _ and - only' }
  if (typeof b.engine !== 'string' || !engines.includes(b.engine)) {
    return { error: `engine must be one of ${engines.join(', ')}, got ${JSON.stringify(b.engine)}` }
  }
  if (typeof b.system_prompt !== 'string') return { error: 'system_prompt is required (it may be empty)' }
  if (typeof b.text !== 'string' || !b.text) return { error: 'text is required' }
  if (b.tools !== undefined && !(Array.isArray(b.tools) && b.tools.every((t) => typeof t === 'string'))) {
    return { error: 'tools must be a list of strings' }
  }
  for (const key of ['mcp_token', 'model', 'effort', 'resume', 'user_email', 'user_name']) {
    if (b[key] !== undefined && typeof b[key] !== 'string') return { error: `${key} must be a string` }
  }
  const opt = (key: string) => (b[key] as string | undefined) || undefined
  return {
    turn: {
      sessionId: b.session_id,
      engine: b.engine,
      text: b.text,
      resume: opt('resume'),
      model: opt('model'),
      effort: opt('effort'),
      tools: b.tools as string[] | undefined,
      systemPrompt: b.system_prompt,
      mcpToken: (b.mcp_token as string | undefined) ?? '',
      cwd,
      userEmail: opt('user_email'),
      userName: opt('user_name'),
    },
  }
}

/**
 * The environment a turn's tools see on top of the container's: who the turn is for, and that
 * person as the author of any commit made during it. The committer is always Bob.
 */
export function turnEnv(turn: Pick<Turn, 'userEmail' | 'userName'>): Record<string, string> {
  const email = turn.userEmail ?? ''
  const name = turn.userName || email
  const env: Record<string, string> = {
    BOB_USER_EMAIL: email,
    BOB_USER_NAME: name,
    GIT_COMMITTER_NAME: 'Bob',
    GIT_COMMITTER_EMAIL: 'bob@badcode.tv',
  }
  if (email) {
    env.GIT_AUTHOR_NAME = name
    env.GIT_AUTHOR_EMAIL = email
  }
  return env
}

export interface TurnResult {
  harnessSessionId?: string;
  error?: string;
}
