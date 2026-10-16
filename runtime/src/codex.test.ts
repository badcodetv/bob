import { test } from 'node:test'
import assert from 'node:assert/strict'
import { EventEmitter } from 'node:events'
import { mkdtempSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { createInterface } from 'node:readline'
import { PassThrough } from 'node:stream'
import { runCodexTurn, type AppServer } from './codex.js'
import type { Turn } from './turn.js'

const turn = (over: Partial<Turn> = {}): Turn => ({
  sessionId: 's1', engine: 'codex', text: 'say hi', systemPrompt: 'You are working inside Bob.',
  mcpToken: 's1.abc', cwd: '/project/work', userEmail: 'kai@example.com', userName: 'Kai', ...over,
})

// A codex home that is logged in (auth.json present) or not.
function home(loggedIn: boolean): string {
  const dir = mkdtempSync(join(tmpdir(), 'bob-codex-'))
  if (loggedIn) writeFileSync(join(dir, 'auth.json'), '{}')
  return dir
}

type Req = { id?: number; method?: string; params?: any; result?: any; error?: any }
type Server = {
  reply: (result: unknown) => void; fail: (message: string) => void; notify: (method: string, params: unknown) => void
  send: (m: unknown) => void; exit: (code: number) => void
}
// Called for every message the driver sends: requests, notifications and its answers to ours.
type Script = (msg: Req, server: Server) => void

// A fake `codex app-server`: records every message the driver sends and answers it with `script`.
// By default each request gets an empty result.
function fakeAppServer(script: Script = (m, s) => { if (m.method && m.id !== undefined) s.reply({}) }) {
  const seen: { env?: NodeJS.ProcessEnv; sent: Req[]; killed: boolean } = { sent: [], killed: false }
  const appServer = (env: NodeJS.ProcessEnv): AppServer => {
    seen.env = env
    const proc = new EventEmitter() as EventEmitter & AppServer
    const stdin = new PassThrough(), stdout = new PassThrough(), stderr = new PassThrough()
    Object.assign(proc, { stdin, stdout, stderr, kill: () => { seen.killed = true; return true } })
    const write = (m: unknown) => stdout.write(JSON.stringify(m) + '\n')
    createInterface({ input: stdin }).on('line', (line) => {
      const req = JSON.parse(line) as Req
      seen.sent.push(req)
      script(req, {
        reply: (result) => write({ id: req.id, result }),
        fail: (message) => write({ id: req.id, error: { code: -1, message } }),
        notify: (method, params) => write({ method, params }),
        send: write,
        exit: (code) => { stderr.write('boom\n'); setImmediate(() => proc.emit('exit', code)) },
      })
    })
    return proc
  }
  return { appServer, seen, request: (method: string) => seen.sent.find((m) => m.method === method) }
}

// A turn that streams `events` (method, params) after turn/start and then completes with `status`.
function turnScript(threadId: string, events: [string, unknown][], status: { status: string; error?: unknown } = { status: 'completed' }): Script {
  return (req, s) => {
    if (!req.method || req.id === undefined) return
    if (req.method === 'thread/start') {
      s.notify('thread/started', { thread: { id: threadId } })
      return s.reply({ thread: { id: threadId } })
    }
    if (req.method === 'thread/resume') return s.reply({ thread: { id: req.params.threadId } })
    if (req.method !== 'turn/start') return s.reply({})
    s.reply({ turn: { id: 'turn-1', status: 'inProgress' } })
    s.notify('turn/started', { threadId, turn: { id: 'turn-1' } })
    for (const [method, params] of events) s.notify(method, params)
    s.notify('turn/completed', { threadId, turn: { id: 'turn-1', ...status } })
  }
}

const run = (t: Turn, fake: ReturnType<typeof fakeAppServer>, emitted: unknown[] = [], signal = new AbortController().signal) =>
  runCodexTurn(t, 'http://api:8070', (e) => emitted.push(e), signal, { appServer: fake.appServer, home: home(true), project: 'enc' })

test('without auth.json the turn fails with the login command for this project, and Codex never runs', async () => {
  const fake = fakeAppServer()
  const emitted: unknown[] = []
  const result = await runCodexTurn(turn(), 'http://api:8070', (e) => emitted.push(e), new AbortController().signal,
    { appServer: fake.appServer, home: home(false), project: 'enc' })
  assert.deepEqual(result, {
    error: 'Codex is not logged in for this project. On the box run: docker exec -it bob-project-enc codex login --device-auth',
  })
  assert.equal(fake.seen.env, undefined)
  assert.deepEqual(emitted, [])
})

test('every notification, token deltas included, is passed through unchanged; the thread id comes from the thread', async () => {
  const fake = fakeAppServer(turnScript('th-1', [
    ['item/agentMessage/delta', { itemId: 'm1', delta: 'h' }],
    ['item/agentMessage/delta', { itemId: 'm1', delta: 'i' }],
    ['item/completed', { item: { id: 'm1', type: 'agentMessage', text: 'hi' } }],
  ]))
  const emitted: unknown[] = []
  const result = await run(turn({ model: 'gpt-5.5', effort: 'high' }), fake, emitted)

  assert.deepEqual(result, { harnessSessionId: 'th-1' })
  assert.deepEqual(emitted.map((e: any) => e.method), [
    'thread/started', 'turn/started', 'item/agentMessage/delta', 'item/agentMessage/delta', 'item/completed', 'turn/completed',
  ])
  assert.deepEqual(emitted[2], { method: 'item/agentMessage/delta', params: { itemId: 'm1', delta: 'h' } })
  assert.deepEqual(fake.seen.sent.slice(0, 2).map((m) => m.method), ['initialize', 'initialized'])
  // Bob's prompt, Bob's MCP server with this chat's token, full access, live web search.
  assert.deepEqual(fake.request('thread/start')?.params, {
    cwd: '/project/work', sandbox: 'danger-full-access', approvalPolicy: 'never',
    developerInstructions: 'You are working inside Bob.',
    config: { web_search: 'live', mcp_servers: { bob: { url: 'http://api:8070/mcp', http_headers: { Authorization: 'Bearer s1.abc' } } } },
  })
  assert.deepEqual(fake.request('turn/start')?.params, {
    threadId: 'th-1', input: [{ type: 'text', text: 'say hi' }], model: 'gpt-5.5', effort: 'high',
  })
  // The person in the environment, on top of the container's own.
  assert.equal(fake.seen.env?.BOB_USER_EMAIL, 'kai@example.com')
  assert.equal(fake.seen.env?.PATH, process.env.PATH)
  assert.equal(fake.seen.killed, true)
})

test('a turn with resume continues that thread', async () => {
  const fake = fakeAppServer(turnScript('th-1', []))
  const result = await run(turn({ resume: 'th-1' }), fake)
  assert.equal(fake.request('thread/resume')?.params.threadId, 'th-1')
  assert.equal(fake.request('thread/resume')?.params.excludeTurns, true)
  assert.equal(fake.request('thread/start'), undefined)
  assert.deepEqual(result, { harnessSessionId: 'th-1' })
})

test('a failed turn becomes the error; a retry notice the turn recovers from does not', async () => {
  const retry: [string, unknown] = ['error', { error: { message: 'Reconnecting... 1/5' }, willRetry: true }]
  assert.deepEqual(
    await run(turn(), fakeAppServer(turnScript('th-2', [retry], { status: 'failed', error: { message: 'usage limit reached' } }))),
    { harnessSessionId: 'th-2', error: 'usage limit reached' })
  assert.deepEqual(await run(turn(), fakeAppServer(turnScript('th-3', [retry]))), { harnessSessionId: 'th-3' })
})

test('a request the server refuses, or a server that dies, fails the turn but keeps the thread id', async () => {
  const refused = fakeAppServer((req, s) => {
    if (!req.method || req.id === undefined) return
    if (req.method === 'thread/start') return s.reply({ thread: { id: 'th-4' } })
    if (req.method === 'turn/start') return s.fail('model not supported')
    s.reply({})
  })
  assert.deepEqual(await run(turn(), refused), { harnessSessionId: 'th-4', error: 'model not supported' })

  const died = fakeAppServer((req, s) => {
    if (!req.method || req.id === undefined) return
    if (req.method === 'thread/start') return s.reply({ thread: { id: 'th-5' } })
    if (req.method === 'turn/start') return s.exit(1)
    s.reply({})
  })
  assert.deepEqual(await run(turn(), died), { harnessSessionId: 'th-5', error: 'codex app-server exited (1) before the turn finished: boom' })
})

test('an approval request is refused rather than left hanging', async () => {
  const fake = fakeAppServer((m, s) => {
    // The driver's answer to the approval request: the turn carries on and completes.
    if (m.id === 99 && !m.method) return s.notify('turn/completed', { turn: { id: 'turn-1', status: 'completed' } })
    if (!m.method || m.id === undefined) return
    if (m.method === 'thread/start') return s.reply({ thread: { id: 'th-6' } })
    s.reply({ turn: { id: 'turn-1' } })
    if (m.method === 'turn/start') s.send({ id: 99, method: 'item/commandExecution/requestApproval', params: {} })
  })
  assert.deepEqual(await run(turn(), fake), { harnessSessionId: 'th-6' })
  assert.match(fake.seen.sent.find((m) => m.id === 99)?.error?.message ?? '', /unattended/)
})

test('cancelling the turn interrupts it and stops the server', async () => {
  const abort = new AbortController()
  const fake = fakeAppServer((req, s) => {
    if (!req.method || req.id === undefined) return
    if (req.method === 'thread/start') return s.reply({ thread: { id: 'th-7' } })
    if (req.method === 'turn/start') {
      s.reply({ turn: { id: 'turn-1' } })
      s.notify('turn/started', { turn: { id: 'turn-1' } })
      return setImmediate(() => abort.abort())
    }
    s.reply({})
  })
  assert.deepEqual(await run(turn(), fake, [], abort.signal), { harnessSessionId: 'th-7', error: 'the turn was cancelled' })
  assert.deepEqual(fake.request('turn/interrupt')?.params, { threadId: 'th-7', turnId: 'turn-1' })
  assert.equal(fake.seen.killed, true)
})
