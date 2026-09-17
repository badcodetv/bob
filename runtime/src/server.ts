// The runtime server: the only process Bob's API talks to inside a project container.
// Every request must carry BOB_RUNTIME_TOKEN as basic-auth password (checkAuth): containers of
// other projects can reach this port over the Docker network, and must not be able to use it.
//
//   GET  /health
//   GET  /workers                      the project's workers, read from git, plus the last sync
//   POST /sync                         pull the project's git folder again
//   DELETE /sessions/<id>              remove a session's worktree and branch
//   GET  /files/<path>                 a file from the synced checkout, or a directory's {entries}
//   POST /turns {session_id, worker, text, resume?, model?, effort?, user_email?, user_name?}
//        → application/x-ndjson: {"engine", "event"} per harness event, then
//          {"done": true, "harness_session_id"} or {"done": true, "error"}
import { createServer, type IncomingMessage, type ServerResponse } from 'node:http';
import { readFile } from 'node:fs/promises';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { pipeline } from 'node:stream';
import { join } from 'node:path';
import { loadWorkers, type Worker } from './workers.js';
import { runClaudeTurn } from './claude.js';
import { prepareWorkdir, removeWorkdir } from './workdir.js';
import { listDir, openFile, resolveRepoPath } from './files.js';
import { checkAuth } from './auth.js';
import type { Turn, TurnResult } from './turn.js';

const PORT = Number(process.env.PORT ?? 8080);
const TOKEN = process.env.BOB_RUNTIME_TOKEN ?? '';
// Nothing started from here (harnesses, their tools, git sync) inherits the token.
delete process.env.BOB_RUNTIME_TOKEN;
if (!TOKEN) {
  console.error('BOB_RUNTIME_TOKEN is not set; refusing to serve');
  process.exit(1);
}
const PROJECT_DIR = process.env.BOB_PROJECT_DIR ?? '/project';
const CONFIG_DIR = join(PROJECT_DIR, 'repo', process.env.BOB_REPO_SUBFOLDER ?? '');

type Driver = (worker: Worker, turn: Turn, emit: (event: unknown) => void, signal: AbortSignal) => Promise<TurnResult>;
const drivers: Partial<Record<Worker['engine'], Driver>> = { claude: runClaudeTurn };

createServer((req, res) => {
  handle(req, res).catch((err) => {
    console.error(err);
    if (!res.headersSent) sendJSON(res, 500, { error: String(err?.message ?? err) });
    else res.end();
  });
}).listen(PORT, () => console.log(`bob-runtime listening on :${PORT}, config ${CONFIG_DIR}`));

async function handle(req: IncomingMessage, res: ServerResponse) {
  if (!checkAuth(req.headers.authorization, TOKEN)) return sendJSON(res, 401, { error: 'unauthorized' });
  if (req.method === 'GET' && req.url === '/health') return sendJSON(res, 200, { ok: true });
  if (req.method === 'GET' && req.url === '/workers') return sendJSON(res, 200, await workers());
  if (req.method === 'POST' && req.url === '/sync') {
    // One sync at a time: concurrent fetches into one checkout fail on git's locks, and each caller
    // must read the status its own sync wrote.
    const result = syncing.then(async () => {
      await promisify(execFile)('/bin/sh', ['/app/sync.sh']);
      return workers();
    });
    syncing = result.catch(() => {});
    return sendJSON(res, 200, await result);
  }
  if (req.method === 'POST' && req.url === '/turns') return turn(req, res);
  if (req.method === 'GET' && (req.url === '/files' || req.url?.startsWith('/files/') || req.url?.startsWith('/files?'))) return files(req, res);
  const del = /^\/sessions\/([\w-]+)$/.exec(req.url ?? '');
  if (req.method === 'DELETE' && del) {
    await removeWorkdir(PROJECT_DIR, join(PROJECT_DIR, 'repo'), del[1]);
    return sendJSON(res, 200, { ok: true });
  }
  sendJSON(res, 404, { error: 'not found' });
}

async function files(req: IncomingMessage, res: ServerResponse) {
  const found = await resolveRepoPath(join(PROJECT_DIR, 'repo'), (req.url ?? '').replace(/^\/files\/?/, ''));
  if (!found) return sendJSON(res, 404, { error: 'not found' });
  if (found.kind === 'dir') return sendJSON(res, 200, await listDir(join(PROJECT_DIR, 'repo'), found.path));
  res.writeHead(200, { 'content-type': found.type, 'content-length': found.size });
  // pipeline closes the file when the client goes away early; pipe() would leave it open.
  pipeline(openFile(found.path), res, () => {});
}

let syncing: Promise<unknown> = Promise.resolve();

async function workers() {
  const sync = JSON.parse(await readFile(join(PROJECT_DIR, '.bob', 'sync.json'), 'utf8').catch(() => '{"ok":false,"error":"never synced"}'));
  try {
    return { sync, workers: await loadWorkers(join(CONFIG_DIR, 'workers')) };
  } catch (err) {
    return { sync, workers: [], error: String((err as Error)?.message ?? err) };
  }
}

async function turn(req: IncomingMessage, res: ServerResponse) {
  const body = JSON.parse(await readBody(req)) as { session_id?: string; worker?: string; text?: string; resume?: string; model?: string; effort?: string; user_email?: string; user_name?: string };
  if (!body.session_id || !/^[\w-]+$/.test(body.session_id) || !body.worker || !body.text) {
    return sendJSON(res, 400, { error: 'session_id, worker and text are required' });
  }
  const worker = (await loadWorkers(join(CONFIG_DIR, 'workers'))).find((w) => w.name === body.worker);
  if (!worker) return sendJSON(res, 404, { error: `no worker named ${body.worker}` });
  const driver = drivers[worker.engine];
  if (!driver) return sendJSON(res, 501, { error: `engine ${worker.engine} is not supported yet` });

  const cwd = await prepareWorkdir(PROJECT_DIR, join(PROJECT_DIR, 'repo'), body.session_id);

  const abort = new AbortController();
  res.on('close', () => { if (!res.writableFinished) abort.abort(); });
  res.writeHead(200, { 'content-type': 'application/x-ndjson' });
  const line = (v: unknown) => res.write(JSON.stringify(v) + '\n');

  try {
    const result = await driver(worker, { sessionId: body.session_id, text: body.text, resume: body.resume, model: body.model, effort: body.effort, cwd,
      userEmail: body.user_email, userName: body.user_name },
      (event) => line({ engine: worker.engine, event }), abort.signal);
    line({ done: true, harness_session_id: result.harnessSessionId, error: result.error });
  } catch (err) {
    line({ done: true, error: String((err as Error)?.message ?? err) });
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
