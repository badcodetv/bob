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
}

export interface TurnResult {
  harnessSessionId?: string;
  error?: string;
}
