import { test } from 'node:test'
import assert from 'node:assert/strict'
import { execFileSync } from 'node:child_process'
import { mkdtempSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { parseTurn, turnEnv } from './turn.js'

test('a commit made in a turn is authored by the person and committed by Bob', () => {
  const repo = mkdtempSync(join(tmpdir(), 'bob-turn-'))
  const env = { ...process.env, ...turnEnv({ userEmail: 'kai@example.com', userName: 'Kai' }) }
  const git = (...args: string[]) => execFileSync('git', args, { cwd: repo, env, encoding: 'utf8' }).trim()
  git('init', '-q')
  git('config', 'user.name', 'Someone Else')
  git('config', 'user.email', 'else@example.com')
  git('commit', '-q', '--allow-empty', '-m', 't')
  assert.equal(git('log', '-1', '--format=%an <%ae> / %cn <%ce>'), 'Kai <kai@example.com> / Bob <bob@badcode.tv>')
  assert.equal(execFileSync('sh', ['-c', 'echo $BOB_USER_EMAIL'], { env, encoding: 'utf8' }).trim(), 'kai@example.com')
})

test('a scheduled turn is named by its schedule; with no person the author is left to git config', () => {
  assert.deepEqual(turnEnv({ userEmail: 'schedule:abc', userName: 'daily' }), {
    BOB_USER_EMAIL: 'schedule:abc', BOB_USER_NAME: 'daily',
    GIT_COMMITTER_NAME: 'Bob', GIT_COMMITTER_EMAIL: 'bob@badcode.tv',
    GIT_AUTHOR_NAME: 'daily', GIT_AUTHOR_EMAIL: 'schedule:abc',
  })
  assert.equal(turnEnv({}).GIT_AUTHOR_EMAIL, undefined)
  assert.equal(turnEnv({ userEmail: 'kai@example.com' }).BOB_USER_NAME, 'kai@example.com')
})

const engines = ['claude']
const body = {
  session_id: 'c0ffee-1', engine: 'claude', model: 'claude-opus-4-1', effort: 'high', tools: ['Read', 'Bash(git:*)'],
  system_prompt: 'You are working inside Bob.', mcp_token: '', text: 'hello', resume: 'h-1',
  user_email: 'kai@example.com', user_name: 'Kai',
}

test('a turn carries everything it runs with, and always runs in the shared work folder', () => {
  assert.deepEqual(parseTurn(body, engines, '/project/work'), {
    turn: {
      sessionId: 'c0ffee-1', engine: 'claude', model: 'claude-opus-4-1', effort: 'high', tools: ['Read', 'Bash(git:*)'],
      systemPrompt: 'You are working inside Bob.', mcpToken: '', text: 'hello', resume: 'h-1', cwd: '/project/work',
      userEmail: 'kai@example.com', userName: 'Kai',
    },
  })
  // Only what is required: no tools means the harness default, and no MCP token until Bob sends one.
  const { turn } = parseTurn({ session_id: 's', engine: 'claude', system_prompt: '', text: 'hi' }, engines, '/w') as { turn: { tools?: string[]; mcpToken: string; systemPrompt: string } }
  assert.equal(turn.tools, undefined)
  assert.equal(turn.mcpToken, '')
  assert.equal(turn.systemPrompt, '', 'an empty prompt is a prompt: the API composed nothing to add')
})

test('a turn Bob could not have meant is refused, saying why', () => {
  const refused = (change: Record<string, unknown>, why: RegExp) => {
    const got = parseTurn({ ...body, ...change }, engines, '/project/work')
    assert.ok('error' in got && why.test(got.error), `${JSON.stringify(change)} → ${JSON.stringify(got)}`)
  }
  refused({ engine: 'codex' }, /engine must be one of claude/)
  refused({ engine: 'opencode' }, /engine/)
  refused({ engine: undefined }, /engine/)
  refused({ system_prompt: undefined }, /system_prompt/)
  refused({ system_prompt: 42 }, /system_prompt/)
  refused({ session_id: undefined }, /session_id/)
  refused({ session_id: '../etc' }, /session_id/)
  refused({ text: '' }, /text/)
  refused({ tools: 'Read' }, /tools/)
  refused({ tools: ['Read', 3] }, /tools/)
  refused({ mcp_token: 7 }, /mcp_token/)
  refused({ model: 7 }, /model/)
  for (const notAnObject of [null, 'turn', [], 3]) {
    assert.ok('error' in parseTurn(notAnObject, engines, '/w'), JSON.stringify(notAnObject))
  }
})
