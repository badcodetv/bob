// The Codex driver: one turn = one `codex app-server` process, spoken to in JSON-RPC over stdio.
// The thread is started, or resumed by id, then one turn/start runs the message. Every notification
// the server sends is passed through exactly as it emits it ({method, params}), token deltas
// (item/agentMessage/delta) included — that is why this is app-server and not `codex exec`, whose
// JSON output only says when an item starts and finishes. As with Claude, everything the turn runs
// with — model, effort and the system prompt — comes in the turn. (turn.tools is Claude's
// allowedTools and is ignored here: Codex runs with full access.)
//
// Codex signs in with a ChatGPT account, stored as auth.json in $CODEX_HOME on the project's volume
// (Decision 7). Kai runs `codex login --device-auth` in the container once per project; Codex
// refreshes the token itself from then on.
import { spawn, type ChildProcessByStdio } from 'node:child_process';
import { existsSync } from 'node:fs';
import { homedir } from 'node:os';
import { dirname, join } from 'node:path';
import { createInterface } from 'node:readline';
import type { Readable, Writable } from 'node:stream';
import { fileURLToPath } from 'node:url';
import { turnEnv, type Turn, type TurnResult } from './turn.js';
import { bobMcp } from './mcp.js';

// The CLI from the @openai/codex package: dist/codex.js → node_modules/.bin/codex.
const CODEX_BIN = join(dirname(fileURLToPath(import.meta.url)), '..', 'node_modules', '.bin', 'codex');

/** The part of a child process the driver uses, so tests can run a turn without the CLI. */
export type AppServer = Pick<ChildProcessByStdio<Writable, Readable, Readable>, 'stdin' | 'stdout' | 'stderr' | 'kill' | 'once'>;

export interface CodexDeps {
  /** Starts `codex app-server` with this environment. */
  appServer?: (env: NodeJS.ProcessEnv) => AppServer;
  /** $CODEX_HOME, where auth.json lives. */
  home?: string;
  /** This project's name, for the login command in the not-logged-in error. */
  project?: string;
}

type Message = { id?: number; method?: string; params?: any; result?: any; error?: { message: string } };

export async function runCodexTurn(turn: Turn, apiUrl: string, emit: (event: unknown) => void, signal: AbortSignal, deps: CodexDeps = {}): Promise<TurnResult> {
  const home = deps.home ?? process.env.CODEX_HOME ?? join(homedir(), '.codex');
  if (!existsSync(join(home, 'auth.json'))) {
    // Without this, Codex would retry the OpenAI API unauthenticated five times and fail with a 401.
    const project = deps.project ?? process.env.BOB_PROJECT_NAME ?? '<name>';
    return { error: `Codex is not logged in for this project. On the box run: docker exec -it bob-project-${project} codex login --device-auth` };
  }

  const server = (deps.appServer ?? startAppServer)({ ...process.env, ...turnEnv(turn) });
  const pending = new Map<number, { resolve: (v: any) => void; reject: (e: Error) => void }>();
  let nextId = 0;
  const send = (m: Message) => server.stdin.write(JSON.stringify(m) + '\n');
  const request = (method: string, params: unknown) => new Promise<any>((resolve, reject) => {
    const id = ++nextId;
    pending.set(id, { resolve, reject });
    send({ id, method, params });
  });

  let harnessSessionId = turn.resume;
  let turnId: string | undefined;
  let error: string | undefined;
  let finish!: (r: TurnResult) => void;
  const finished = new Promise<TurnResult>((resolve) => { finish = resolve; });
  const done = (r: TurnResult) => {
    for (const p of pending.values()) p.reject(new Error(r.error ?? 'the turn ended'));
    pending.clear();
    finish(r);
  };

  // The last lines Codex wrote to stderr, for the error when it exits mid-turn.
  let stderr = '';
  server.stderr.on('data', (d: Buffer) => { stderr = (stderr + d.toString()).slice(-2000); });
  server.once('exit', (code: number | null) => {
    done({ harnessSessionId, error: `codex app-server exited (${code ?? 'killed'}) before the turn finished${stderr.trim() ? `: ${stderr.trim().split('\n').at(-1)}` : ''}` });
  });
  server.stdin.on('error', () => {}); // a write after the server died; 'exit' reports it.

  createInterface({ input: server.stdout }).on('line', (line) => {
    let m: Message;
    try { m = JSON.parse(line); } catch { return; }
    if (m.id !== undefined && !m.method) {
      // A response to one of our requests.
      const p = pending.get(m.id);
      pending.delete(m.id);
      if (m.error) p?.reject(new Error(m.error.message));
      else p?.resolve(m.result);
      return;
    }
    if (m.id !== undefined) {
      // A request from the server (an approval, a question for the user). Nobody is there to answer:
      // approvalPolicy 'never' means these should not come, and refusing is safer than guessing.
      send({ id: m.id, error: { code: -32601, message: 'Bob runs Codex unattended; nothing can answer this request' } } as Message);
      return;
    }
    const p = m.params ?? {};
    switch (m.method) {
      case 'thread/started': harnessSessionId = p.thread?.id ?? harnessSessionId; break;
      case 'turn/started': turnId ??= p.turn?.id; break;
      // An error the server will retry ("Reconnecting... 1/5") only fails the turn if nothing
      // completes after it; one it will not retry does.
      case 'error': if (!p.willRetry) error = p.error?.message; break;
    }
    emit(m);
    if (m.method === 'turn/completed' && (!turnId || p.turn?.id === turnId)) {
      const t = p.turn ?? {};
      if (t.status === 'failed') error = t.error?.message ?? error ?? 'the turn failed';
      else if (t.status === 'interrupted') error = 'the turn was interrupted';
      else error = undefined;
      done(error ? { harnessSessionId, error } : { harnessSessionId });
    }
  });

  const onAbort = () => {
    if (harnessSessionId && turnId) request('turn/interrupt', { threadId: harnessSessionId, turnId }).catch(() => {});
    done({ harnessSessionId, error: 'the turn was cancelled' });
  };
  if (signal.aborted) onAbort();
  else signal.addEventListener('abort', onAbort, { once: true });

  const mcp = bobMcp(apiUrl, turn.mcpToken);
  const threadParams = {
    cwd: turn.cwd,
    // The container is the sandbox, and there is no one to ask for approval mid-turn.
    sandbox: 'danger-full-access',
    approvalPolicy: 'never',
    // Bob's prompt (the environment note, the project prompt and the worker prompt, composed by the
    // API) goes on top of Codex's own instructions as developer instructions.
    ...(turn.systemPrompt ? { developerInstructions: turn.systemPrompt } : {}),
    // Config overrides for this thread only; nothing is written to $CODEX_HOME/config.toml.
    config: { web_search: 'live', mcp_servers: { bob: { url: mcp.url, http_headers: mcp.headers } } },
  };

  (async () => {
    await request('initialize', { clientInfo: { name: 'bob', version: '1' }, capabilities: null });
    send({ method: 'initialized' });
    const started = turn.resume
      ? await request('thread/resume', { threadId: turn.resume, excludeTurns: true, ...threadParams })
      : await request('thread/start', threadParams);
    harnessSessionId = started?.thread?.id ?? harnessSessionId;
    const res = await request('turn/start', {
      threadId: harnessSessionId,
      input: [{ type: 'text', text: turn.text }],
      ...(turn.model ? { model: turn.model } : {}),
      ...(turn.effort ? { effort: turn.effort } : {}),
    });
    turnId ??= res?.turn?.id;
  })().catch((err) => done({ harnessSessionId, error: String((err as Error)?.message ?? err) }));

  try {
    // Keep the thread id even when the turn fails, so the next turn resumes the same conversation.
    return await finished;
  } finally {
    signal.removeEventListener('abort', onAbort);
    server.stdin.end();
    server.kill();
  }
}

function startAppServer(env: NodeJS.ProcessEnv): AppServer {
  return spawn(CODEX_BIN, ['app-server'], { env, stdio: ['pipe', 'pipe', 'pipe'] });
}
