// bin/bob-push against throwaway repositories: a bare "remote" and two clones of it standing in
// for two chats (or a chat and a person) pushing to the same branch.
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { spawnSync } from 'node:child_process'
import { chmodSync, existsSync, mkdtempSync, readFileSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'

// dist/bobpush.test.js → runtime/bin/bob-push
const BOB_PUSH = fileURLToPath(new URL('../bin/bob-push', import.meta.url))

const ENV = {
  ...process.env,
  GIT_CONFIG_GLOBAL: '/dev/null',
  GIT_CONFIG_NOSYSTEM: '1',
  GIT_AUTHOR_NAME: 'T', GIT_AUTHOR_EMAIL: 't@example.com',
  GIT_COMMITTER_NAME: 'T', GIT_COMMITTER_EMAIL: 't@example.com',
  BOB_PUSH_ATTEMPTS: '3',
}

function git(cwd: string, ...args: string[]): string {
  const r = spawnSync('git', args, { cwd, env: ENV, encoding: 'utf8' })
  if (r.status !== 0) throw new Error(`git ${args.join(' ')} in ${cwd}: ${r.stderr}`)
  return r.stdout.trim()
}

function bobPush(cwd: string, ...args: string[]) {
  return spawnSync(BOB_PUSH, args, { cwd, env: ENV, encoding: 'utf8' })
}

function commit(repo: string, file: string, text: string, msg = `edit ${file}`): void {
  writeFileSync(join(repo, file), text)
  git(repo, 'add', file)
  git(repo, 'commit', '-q', '-m', msg)
}

/** A bare remote with one commit on main, and two clones of it. */
function setup(): { remote: string; a: string; b: string } {
  const root = mkdtempSync(join(tmpdir(), 'bob-push-'))
  const remote = join(root, 'remote.git')
  const seed = join(root, 'seed')
  git(root, 'init', '-q', '--bare', '-b', 'main', remote)
  git(root, 'init', '-q', '-b', 'main', seed)
  commit(seed, 'shared.txt', 'one\ntwo\nthree\n', 'seed')
  git(seed, 'remote', 'add', 'origin', remote)
  git(seed, 'push', '-q', 'origin', 'main')
  const a = join(root, 'a')
  const b = join(root, 'b')
  git(root, 'clone', '-q', remote, a)
  git(root, 'clone', '-q', remote, b)
  return { remote, a, b }
}

const remoteLog = (remote: string) => git(remote, 'log', '--format=%s', 'main').split('\n')

test('pushes when nobody else has pushed', () => {
  const { remote, a } = setup()
  commit(a, 'a.txt', 'a\n')
  const r = bobPush(a, 'main')
  assert.equal(r.status, 0, r.stderr)
  assert.deepEqual(remoteLog(remote), ['edit a.txt', 'seed'])
})

test('rebases onto a push made in the meantime, then pushes (branch defaults to the current one)', () => {
  const { remote, a, b } = setup()
  commit(b, 'b.txt', 'b\n')
  git(b, 'push', '-q', 'origin', 'main')
  commit(a, 'a.txt', 'a\n')
  const r = bobPush(a)
  assert.equal(r.status, 0, r.stderr)
  assert.deepEqual(remoteLog(remote), ['edit a.txt', 'edit b.txt', 'seed'])
})

test('retries when someone pushes between its pull and its push', () => {
  const { remote, a, b } = setup()
  commit(b, 'b.txt', 'b\n')
  commit(a, 'a.txt', 'a\n')
  // A pre-push hook in a that, the first time only, lets b win the race.
  const marker = join(a, '.git', 'raced')
  const hook = join(a, '.git', 'hooks', 'pre-push')
  writeFileSync(hook, `#!/bin/sh\n[ -e '${marker}' ] && exit 0\ntouch '${marker}'\ngit -C '${b}' push -q origin main\n`)
  chmodSync(hook, 0o755)
  const r = bobPush(a, 'main')
  assert.equal(r.status, 0, r.stderr)
  assert.ok(existsSync(marker), 'the race happened')
  assert.match(r.stderr, /push rejected \(someone pushed first\)/)
  assert.deepEqual(remoteLog(remote), ['edit a.txt', 'edit b.txt', 'seed'])
})

test('on a conflict: aborts the rebase, names the files, exits 1 and leaves the commit as it was', () => {
  const { remote, a, b } = setup()
  commit(b, 'shared.txt', 'one\nTWO from b\nthree\n')
  git(b, 'push', '-q', 'origin', 'main')
  commit(a, 'shared.txt', 'one\nTWO from a\nthree\n')
  const before = git(a, 'rev-parse', 'HEAD')
  const r = bobPush(a, 'main')
  assert.equal(r.status, 1)
  assert.match(r.stderr, /conflict/)
  assert.match(r.stderr, /^  shared\.txt$/m)
  assert.equal(git(a, 'rev-parse', 'HEAD'), before)
  assert.equal(git(a, 'status', '--porcelain'), '')
  assert.ok(!existsSync(join(a, '.git', 'rebase-merge')) && !existsSync(join(a, '.git', 'rebase-apply')))
  assert.equal(readFileSync(join(a, 'shared.txt'), 'utf8'), 'one\nTWO from a\nthree\n')
  assert.deepEqual(remoteLog(remote), ['edit shared.txt', 'seed'])
})

test("keeps another chat's uncommitted changes in the shared checkout", () => {
  const { remote, a, b } = setup()
  commit(b, 'b.txt', 'b\n')
  git(b, 'push', '-q', 'origin', 'main')
  commit(a, 'a.txt', 'a\n')
  writeFileSync(join(a, 'shared.txt'), 'one\ntwo\nthree\nhalf-written by someone else\n')
  writeFileSync(join(a, 'draft.txt'), 'untracked\n')
  const r = bobPush(a, 'main')
  assert.equal(r.status, 0, r.stderr)
  assert.deepEqual(remoteLog(remote), ['edit a.txt', 'edit b.txt', 'seed'])
  assert.match(readFileSync(join(a, 'shared.txt'), 'utf8'), /half-written/)
  assert.ok(existsSync(join(a, 'draft.txt')))
})

test('refuses a branch other than the current one, and a folder that is not a repository', () => {
  const { a } = setup()
  const r = bobPush(a, 'other')
  assert.equal(r.status, 2)
  assert.match(r.stderr, /you are on 'main', not 'other'/)
  const notRepo = mkdtempSync(join(tmpdir(), 'bob-push-none-'))
  assert.equal(bobPush(notRepo).status, 2)
})

test('fails without retrying when the push fails for another reason', () => {
  const { a } = setup()
  commit(a, 'a.txt', 'a\n')
  git(a, 'remote', 'set-url', 'origin', join(tmpdir(), 'no-such-remote.git'))
  const r = bobPush(a, 'main')
  assert.equal(r.status, 3)
  assert.doesNotMatch(r.stderr, /trying again/)
})
