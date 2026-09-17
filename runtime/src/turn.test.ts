import { test } from 'node:test'
import assert from 'node:assert/strict'
import { execFileSync } from 'node:child_process'
import { mkdtempSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { turnEnv } from './turn.js'

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
