export interface Turn {
  sessionId: string;
  text: string;
  /** The harness's own session id from the previous turn; absent on the first turn. */
  resume?: string;
  /** Per-session overrides of the worker's model and effort. */
  model?: string;
  effort?: string;
  /** Working directory for this session, on the project volume. */
  cwd: string;
  /** Who the turn runs for: a signed-in email, or "schedule:<id>" for a scheduled turn. */
  userEmail?: string;
  userName?: string;
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
