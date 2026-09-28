import { test } from 'node:test'
import assert from 'node:assert/strict'
import { mkdtempSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import type { CodexOptions, ThreadEvent, ThreadOptions, TurnOptions } from '@openai/codex-sdk'
import { runCodexTurn } from './codex.js'
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

// A fake Codex client: records how it was made and run, and streams the given events.
function fakeCodex(events: ThreadEvent[], opts: { throws?: Error } = {}) {
  const seen: { options?: CodexOptions; start?: ThreadOptions; resume?: [string, ThreadOptions]; input?: string; turnOptions?: TurnOptions } = {}
  const thread = {
    async runStreamed(input: string, turnOptions?: TurnOptions) {
      seen.input = input
      seen.turnOptions = turnOptions
      return {
        events: (async function* () {
          for (const e of events) yield e
          if (opts.throws) throw opts.throws
        })(),
      }
    },
  }
  const codex = (options: CodexOptions) => {
    seen.options = options
    return {
      startThread(o: ThreadOptions) { seen.start = o; return thread },
      resumeThread(id: string, o: ThreadOptions) { seen.resume = [id, o]; return thread },
    }
  }
  return { codex, seen }
}

test('without auth.json the turn fails with the login command for this project, and Codex never runs', async () => {
  const { codex, seen } = fakeCodex([])
  const emitted: unknown[] = []
  const result = await runCodexTurn(turn(), 'http://api:8070', (e) => emitted.push(e), new AbortController().signal,
    { codex, home: home(false), project: 'enc' })
  assert.deepEqual(result, {
    error: 'Codex is not logged in for this project. On the box run: docker exec -it bob-project-enc codex login --device-auth',
  })
  assert.equal(seen.options, undefined)
  assert.deepEqual(emitted, [])
})

test('every event is passed through unchanged, and the thread id comes from thread.started', async () => {
  const events: ThreadEvent[] = [
    { type: 'thread.started', thread_id: 'th-1' },
    { type: 'turn.started' },
    { type: 'item.completed', item: { id: 'i1', type: 'agent_message', text: 'hi' } },
    { type: 'turn.completed', usage: { input_tokens: 1, cached_input_tokens: 0, cache_write_input_tokens: 0, output_tokens: 1, reasoning_output_tokens: 0 } },
  ]
  const { codex, seen } = fakeCodex(events)
  const emitted: unknown[] = []
  const signal = new AbortController().signal
  const result = await runCodexTurn(turn({ model: 'gpt-5.5', effort: 'high' }), 'http://api:8070', (e) => emitted.push(e), signal,
    { codex, home: home(true), project: 'enc' })

  assert.deepEqual(result, { harnessSessionId: 'th-1' })
  assert.deepEqual(emitted, events)
  assert.equal(seen.input, 'say hi')
  assert.equal(seen.turnOptions?.signal, signal)
  // Bob's prompt, Bob's MCP server with this chat's token, the person in the environment.
  assert.deepEqual(seen.options?.config, {
    developer_instructions: 'You are working inside Bob.',
    mcp_servers: { bob: { url: 'http://api:8070/mcp', http_headers: { Authorization: 'Bearer s1.abc' } } },
  })
  assert.equal(seen.options?.env?.BOB_USER_EMAIL, 'kai@example.com')
  assert.equal(seen.options?.env?.PATH, process.env.PATH)
  assert.deepEqual(seen.start, {
    workingDirectory: '/project/work', skipGitRepoCheck: true, sandboxMode: 'danger-full-access',
    approvalPolicy: 'never', webSearchMode: 'live', model: 'gpt-5.5', modelReasoningEffort: 'high',
  })
})

test('a turn with resume continues that thread', async () => {
  const { codex, seen } = fakeCodex([{ type: 'turn.started' }])
  const result = await runCodexTurn(turn({ resume: 'th-1' }), 'http://api:8070', () => {}, new AbortController().signal,
    { codex, home: home(true), project: 'enc' })
  assert.equal(seen.resume?.[0], 'th-1')
  assert.equal(seen.start, undefined)
  assert.deepEqual(result, { harnessSessionId: 'th-1' })
})

test('turn.failed becomes the error; a retry notice that the turn recovers from does not', async () => {
  const failed = fakeCodex([
    { type: 'thread.started', thread_id: 'th-2' },
    { type: 'error', message: 'Reconnecting... 1/5' },
    { type: 'turn.failed', error: { message: 'usage limit reached' } },
  ])
  assert.deepEqual(
    await runCodexTurn(turn(), 'http://api:8070', () => {}, new AbortController().signal, { codex: failed.codex, home: home(true), project: 'enc' }),
    { harnessSessionId: 'th-2', error: 'usage limit reached' })

  const recovered = fakeCodex([
    { type: 'thread.started', thread_id: 'th-3' },
    { type: 'error', message: 'Reconnecting... 1/5' },
    { type: 'turn.completed', usage: { input_tokens: 1, cached_input_tokens: 0, cache_write_input_tokens: 0, output_tokens: 1, reasoning_output_tokens: 0 } },
  ])
  assert.deepEqual(
    await runCodexTurn(turn(), 'http://api:8070', () => {}, new AbortController().signal, { codex: recovered.codex, home: home(true), project: 'enc' }),
    { harnessSessionId: 'th-3' })
})

test('a stream that ends in an error event, or a CLI that dies, fails the turn but keeps the thread id', async () => {
  const errored = fakeCodex([{ type: 'thread.started', thread_id: 'th-4' }, { type: 'error', message: 'stream disconnected' }])
  assert.deepEqual(
    await runCodexTurn(turn(), 'http://api:8070', () => {}, new AbortController().signal, { codex: errored.codex, home: home(true), project: 'enc' }),
    { harnessSessionId: 'th-4', error: 'stream disconnected' })

  const died = fakeCodex([{ type: 'thread.started', thread_id: 'th-5' }], { throws: new Error('Codex Exec exited with code 1: boom') })
  assert.deepEqual(
    await runCodexTurn(turn(), 'http://api:8070', () => {}, new AbortController().signal, { codex: died.codex, home: home(true), project: 'enc' }),
    { harnessSessionId: 'th-5', error: 'Codex Exec exited with code 1: boom' })
})
