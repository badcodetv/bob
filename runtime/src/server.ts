// The runtime server: the only process Bob's API talks to inside a project container.
// Every request must carry this project's password as basic auth (checkAuth): containers of
// other projects can reach this port over the Docker network, and must not be able to use it.
//
// The password is BOB_RUNTIME_TOKEN, this project's own random value; Bob holds the same value as
// BOB_RUNTIME_TOKEN_<NAME>. It is taken out of the environment at startup, so nothing started from
// here — harnesses, the agent's tools — can read it and drive this server.
//
// The runtime keeps no config: each turn brings its engine, model, effort, tools and system prompt,
// read and composed by Bob's API. Every chat of the project works in one shared folder, /project/work.
//
//   GET  /health
//   GET  /files/<path>                 a file from /project/work, or a directory's {entries}
//   POST /turns {session_id, engine, model?, effort?, tools?, system_prompt, mcp_token, text,
//                resume?, user_email?, user_name?}                                  (see turn.ts)
//        → application/x-ndjson: {"engine", "event"} per harness event, then
//          {"done": true, "harness_session_id"} or {"done": true, "error"}
import { createServer, type IncomingMessage, type ServerResponse } from 'node:http';
import { pipeline } from 'node:stream';
import { join } from 'node:path';
import { runClaudeTurn } from './claude.js';
import { runCodexTurn } from './codex.js';
import { listDir, openFile, resolveRepoPath } from './files.js';
import { checkAuth } from './auth.js';
import { parseTurn, type Turn, type TurnResult } from './turn.js';
import { linkSkills } from './skills.js';

const PORT = Number(process.env.PORT ?? 8080);
const TOKEN = process.env.BOB_RUNTIME_TOKEN ?? '';
// Where Bob's API is, for the MCP server each turn is given (mcp.ts).
const API_URL = process.env.BOB_API_URL ?? '';
// Nothing started from here (harnesses, their tools) inherits the token.
delete process.env.BOB_RUNTIME_TOKEN;
if (!TOKEN || !API_URL) {
  console.error('BOB_RUNTIME_TOKEN and BOB_API_URL are required; refusing to serve');
  process.exit(1);
}

// Draining: a deploy stops this container, and a turn cut off half way is a chat that stops
// mid-sentence and a scheduled run recorded as failed with no retry. On SIGTERM we stop accepting
// turns and let the ones in flight finish. Compose's stop_grace_period must be longer than a turn
// is expected to take, or Docker sends SIGKILL and the wait was pointless.
let draining = false;
let inFlight = 0;
let onDrained: (() => void) | null = null;
const PROJECT_DIR = process.env.BOB_PROJECT_DIR ?? '/project';
const WORK_DIR = join(PROJECT_DIR, 'work');
const SKILLS_DIR = join(PROJECT_DIR, 'skills');

// A skill dropped in /project/skills is picked up by both harnesses: each keeps its own skills
// directory under its home, symlinked here.
const skillTargets = [process.env.CLAUDE_CONFIG_DIR, process.env.CODEX_HOME]
  .filter((home): home is string => Boolean(home))
  .map((home) => join(home, 'skills'));
linkSkills(SKILLS_DIR, skillTargets);

type Driver = (turn: Turn, apiUrl: string, emit: (event: unknown) => void, signal: AbortSignal) => Promise<TurnResult>;
const drivers: Record<string, Driver> = { claude: runClaudeTurn, codex: runCodexTurn };

createServer((req, res) => {
  handle(req, res).catch((err) => {
    console.error(err);
    if (!res.headersSent) sendJSON(res, 500, { error: String(err?.message ?? err) });
    else res.end();
  });
}).listen(PORT, () => console.log(`bob-runtime listening on :${PORT}, working in ${WORK_DIR}, Bob at ${API_URL}`));

for (const signal of ['SIGTERM', 'SIGINT'] as const) {
  process.on(signal, () => {
    if (draining) return;
    draining = true;
    console.log(`${signal}: draining, ${inFlight} turn(s) in flight`);
    if (inFlight === 0) process.exit(0);
    onDrained = () => { console.log('drained'); process.exit(0); };
  });
}

async function handle(req: IncomingMessage, res: ServerResponse) {
  if (!checkAuth(req.headers.authorization, TOKEN)) return sendJSON(res, 401, { error: 'unauthorized' });
  // /health keeps answering while draining, but says so, so Bob can tell "stopping" from "broken".
  if (req.method === 'GET' && req.url === '/health') return sendJSON(res, 200, { ok: true, draining });
  if (req.method === 'POST' && req.url === '/turns') {
    if (draining) return sendJSON(res, 503, { error: 'this project is stopping; try again once it is back' });
    return turn(req, res);
  }
  if (req.method === 'GET' && (req.url === '/files' || req.url?.startsWith('/files/') || req.url?.startsWith('/files?'))) return files(req, res);
  sendJSON(res, 404, { error: 'not found' });
}

async function files(req: IncomingMessage, res: ServerResponse) {
  const found = await resolveRepoPath(WORK_DIR, (req.url ?? '').replace(/^\/files\/?/, ''));
  if (!found) return sendJSON(res, 404, { error: 'not found' });
  if (found.kind === 'dir') return sendJSON(res, 200, await listDir(WORK_DIR, found.path));
  res.writeHead(200, { 'content-type': found.type, 'content-length': found.size });
  // pipeline closes the file when the client goes away early; pipe() would leave it open.
  pipeline(openFile(found.path), res, () => {});
}

async function turn(req: IncomingMessage, res: ServerResponse) {
  let body: unknown;
  try {
    body = JSON.parse(await readBody(req));
  } catch {
    return sendJSON(res, 400, { error: 'the body must be JSON' });
  }
  const parsed = parseTurn(body, Object.keys(drivers), WORK_DIR);
  if ('error' in parsed) return sendJSON(res, 400, { error: parsed.error });
  const { turn } = parsed;
  const driver = drivers[turn.engine];

  const abort = new AbortController();
  res.on('close', () => { if (!res.writableFinished) abort.abort(); });
  res.writeHead(200, { 'content-type': 'application/x-ndjson' });
  const line = (v: unknown) => res.write(JSON.stringify(v) + '\n');

  inFlight++;
  try {
    const result = await driver(turn, API_URL, (event) => line({ engine: turn.engine, event }), abort.signal);
    line({ done: true, harness_session_id: result.harnessSessionId, error: result.error });
  } catch (err) {
    line({ done: true, error: String((err as Error)?.message ?? err) });
  } finally {
    if (--inFlight === 0 && onDrained) onDrained();
  }
  res.end();
}

function sendJSON(res: ServerResponse, status: number, v: unknown) {
  res.writeHead(status, { 'content-type': 'application/json' });
  res.end(JSON.stringify(v));
}

async function readBody(req: IncomingMessage): Promise<string> {
  const chunks: Buffer[] = [];
  for await (const c of req) chunks.push(c as Buffer);
  return Buffer.concat(chunks).toString('utf8');
}
