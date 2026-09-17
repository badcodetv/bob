import { test } from 'node:test'
import assert from 'node:assert/strict'
import { execFileSync } from 'node:child_process'
import { mkdtempSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join, resolve } from 'node:path'

const bobPush = resolve(import.meta.dirname, '..', 'bob-push')
const env = { ...process.env, GIT_AUTHOR_NAME: 't', GIT_AUTHOR_EMAIL: 't@t', GIT_COMMITTER_NAME: 't', GIT_COMMITTER_EMAIL: 't@t' }
const git = (cwd: string, ...args: string[]) => execFileSync('git', args, { cwd, env, encoding: 'utf8' }).trim()

function remoteWithTwoChats() {
  const root = mkdtempSync(join(tmpdir(), 'bob-push-'))
  const remote = join(root, 'remote.git')
  git(root, 'init', '-q', '--bare', '-b', 'main', remote)
  git(root, 'clone', '-q', remote, 'seed')
  writeFileSync(join(root, 'seed', 'a.txt'), 'a\n')
  git(join(root, 'seed'), 'add', '-A')
  git(join(root, 'seed'), 'commit', '-qm', 'seed')
  git(join(root, 'seed'), 'push', '-q', 'origin', 'HEAD:main')
  git(root, 'clone', '-q', remote, 'one')
  git(root, 'clone', '-q', remote, 'two')
  return { root, remote, one: join(root, 'one'), two: join(root, 'two') }
}

function commit(dir: string, file: string, text: string) {
  writeFileSync(join(dir, file), text)
  git(dir, 'add', '-A')
  git(dir, 'commit', '-qm', `write ${file}`)
}

test('two chats push to the same branch: the second rebases and still lands', () => {
  const { remote, one, two } = remoteWithTwoChats()
  commit(one, 'one.txt', 'one\n')
  commit(two, 'two.txt', 'two\n')
  execFileSync('sh', [bobPush, 'main'], { cwd: one, env })
  const out = execFileSync('sh', [bobPush, 'main'], { cwd: two, env, encoding: 'utf8' })
  assert.match(out, /pushed .* to main/)
  assert.equal(git(remote, 'log', '--format=%s', 'main'), 'write two.txt\nwrite one.txt\nseed')
})

test('a conflicting change stops bob-push; after resolving, bob-push finishes the rebase and lands', () => {
  const { remote, one, two } = remoteWithTwoChats()
  commit(one, 'a.txt', 'from one\n')
  commit(two, 'a.txt', 'from two\n')
  execFileSync('sh', [bobPush, 'main'], { cwd: one, env })
  assert.throws(() => execFileSync('sh', [bobPush, 'main'], { cwd: two, env, stdio: 'pipe' }), /conflict in: a.txt/)
  assert.equal(git(remote, 'show', 'main:a.txt'), 'from one')
  assert.throws(() => execFileSync('sh', [bobPush, 'main'], { cwd: two, env, stdio: 'pipe' }), /conflict in: a.txt/, 'still unresolved')

  writeFileSync(join(two, 'a.txt'), 'from both\n')
  git(two, 'add', 'a.txt')
  const out = execFileSync('sh', [bobPush, 'main'], { cwd: two, env, encoding: 'utf8' })
  assert.match(out, /pushed .* to main/)
  assert.equal(git(remote, 'show', 'main:a.txt'), 'from both')
  assert.equal(git(remote, 'log', '--format=%s', 'main'), 'write a.txt\nwrite a.txt\nseed')
})
