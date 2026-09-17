export interface Turn {
  sessionId: string;
  text: string;
  /** The harness's own session id from the previous turn; absent on the first turn. */
  resume?: string;
  /** Working directory for this session, on the project volume. */
  cwd: string;
}

export interface TurnResult {
  harnessSessionId?: string;
  error?: string;
}
