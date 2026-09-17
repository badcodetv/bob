// The runtime server: the only process Bob's API talks to inside a project container.
//
//   GET  /health
//   GET  /workers                      the project's workers, read from git, plus the last sync
//   POST /sync                         pull the project's git folder again
//   DELETE /sessions/<id>              remove a session's worktree and branch
//   POST /turns {session_id, worker, text, resume?, model?, effort?, user_email?, user_name?}
//        → application/x-ndjson: {"engine", "event"} per harness event, then
//          {"done": true, "harness_session_id"} or {"done": true, "error"}
import { createServer, type IncomingMessage, type ServerResponse } from 'node:http';
import { readFile } from 'node:fs/promises';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { join } from 'node:path';
import { loadWorkers, type Worker } from './workers.js';
import { runClaudeTurn } from './claude.js';
import { prepareWorkdir, removeWorkdir } from './workdir.js';
import type { Turn, TurnResult } from './turn.js';

const PORT = Number(process.env.PORT ?? 8080);
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
  if (req.method === 'GET' && req.url === '/health') return sendJSON(res, 200, { ok: true });
  if (req.method === 'GET' && req.url === '/workers') return sendJSON(res, 200, await workers());
  if (req.method === 'POST' && req.url === '/sync') {
    await promisify(execFile)('/bin/sh', ['/app/sync.sh']);
    return sendJSON(res, 200, await workers());
  }
  if (req.method === 'POST' && req.url === '/turns') return turn(req, res);
  const del = /^\/sessions\/([\w-]+)$/.exec(req.url ?? '');
  if (req.method === 'DELETE' && del) {
    await removeWorkdir(PROJECT_DIR, join(PROJECT_DIR, 'repo'), del[1]);
    return sendJSON(res, 200, { ok: true });
  }
  sendJSON(res, 404, { error: 'not found' });
}

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
