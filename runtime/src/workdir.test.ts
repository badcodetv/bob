import { test } from 'node:test'
import assert from 'node:assert/strict'
import { execFileSync } from 'node:child_process'
import { mkdtempSync, mkdirSync, writeFileSync, existsSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { prepareWorkdir, removeWorkdir } from './workdir.js'

const git = (cwd: string, ...args: string[]) =>
  execFileSync('git', ['-c', 'user.name=t', '-c', 'user.email=t@t', ...args], { cwd, encoding: 'utf8' }).trim()

function project() {
  const p = mkdtempSync(join(tmpdir(), 'bob-workdir-'))
  const repo = join(p, 'repo')
  mkdirSync(repo)
  git(repo, 'init', '-q', '-b', 'main')
  writeFileSync(join(repo, 'README.md'), 'hello\n')
  git(repo, 'add', '-A')
  git(repo, 'commit', '-qm', 'first')
  return { p, repo }
}

test('a session gets its own worktree on its own branch', async () => {
  const { p, repo } = project()
  const a = await prepareWorkdir(p, repo, 'aaa')
  const b = await prepareWorkdir(p, repo, 'bbb')
  assert.ok(existsSync(join(a, 'README.md')))
  assert.equal(git(a, 'branch', '--show-current'), 'bob/aaa')
  assert.equal(git(b, 'branch', '--show-current'), 'bob/bbb')

  writeFileSync(join(a, 'README.md'), 'changed in a\n')
  assert.equal(git(b, 'status', '--porcelain'), '', 'a change in one session does not show in another')
})

test('the second turn reuses the worktree', async () => {
  const { p, repo } = project()
  const first = await prepareWorkdir(p, repo, 'ccc')
  writeFileSync(join(first, 'notes.txt'), 'kept\n')
  const second = await prepareWorkdir(p, repo, 'ccc')
  assert.equal(second, first)
  assert.ok(existsSync(join(second, 'notes.txt')))
})

test('a project without a repository gets a plain folder', async () => {
  const p = mkdtempSync(join(tmpdir(), 'bob-workdir-'))
  const dir = await prepareWorkdir(p, join(p, 'repo'), 'ddd')
  assert.ok(existsSync(dir))
  assert.ok(!existsSync(join(dir, '.git')))
})

test('removing a session deletes its worktree and branch', async () => {
  const { p, repo } = project()
  const dir = await prepareWorkdir(p, repo, 'eee')
  await removeWorkdir(p, repo, 'eee')
  assert.ok(!existsSync(dir))
  assert.equal(git(repo, 'branch', '--list', 'bob/eee'), '')
  assert.equal(git(repo, 'worktree', 'list').split('\n').length, 1)
})
