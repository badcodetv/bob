import { test } from 'node:test'
import assert from 'node:assert/strict'
import { mkdtempSync, mkdirSync, writeFileSync, symlinkSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { listDir, resolveRepoPath } from './files.js'

function checkout() {
  const p = mkdtempSync(join(tmpdir(), 'bob-files-'))
  const repo = join(p, 'repo')
  mkdirSync(join(repo, '.git'), { recursive: true })
  writeFileSync(join(repo, '.git', 'config'), '[remote] secret')
  mkdirSync(join(repo, 'site', 'charts'), { recursive: true })
  writeFileSync(join(repo, 'site', 'index.html'), '<h1>hi</h1>')
  writeFileSync(join(repo, 'site', 'charts', 'gold chart.svg'), '<svg/>')
  writeFileSync(join(repo, 'data.bin'), 'x')
  writeFileSync(join(p, 'outside.txt'), 'secret')
  symlinkSync(join(p, 'outside.txt'), join(repo, 'site', 'escape.txt'))
  symlinkSync(p, join(repo, 'site', 'escape-dir'))
  symlinkSync(join(repo, '.git'), join(repo, 'site', 'git-link'))
  symlinkSync('index.html', join(repo, 'site', 'home.html'))
  return repo
}

test('files inside the checkout resolve, with a content type', async () => {
  const repo = checkout()
  const html = await resolveRepoPath(repo, 'site/index.html')
  assert.equal(html?.kind, 'file')
  assert.equal(html?.kind === 'file' && html.type, 'text/html; charset=utf-8')
  const svg = await resolveRepoPath(repo, 'site/charts/gold%20chart.svg?v=2')
  assert.equal(svg?.kind === 'file' && svg.type, 'image/svg+xml')
  const bin = await resolveRepoPath(repo, 'data.bin')
  assert.equal(bin?.kind === 'file' && bin.type, 'application/octet-stream')
  assert.equal((await resolveRepoPath(repo, 'site/home.html'))?.kind, 'file', 'a symlink staying inside is fine')
  assert.equal((await resolveRepoPath(repo, ''))?.kind, 'dir')
  assert.equal((await resolveRepoPath(repo, 'site/'))?.kind, 'dir')
})

test('anything leaving the checkout, or into .git, is not found', async () => {
  const repo = checkout()
  for (const path of [
    '../outside.txt', 'site/../../outside.txt', '%2e%2e/outside.txt', '%2E%2E/outside.txt', 'site/%2e%2e/%2e%2e/outside.txt',
    '..%2foutside.txt', '..%5coutside.txt', '%2fetc%2fpasswd', 'site/escape.txt', 'site/escape-dir/outside.txt', 'site/escape-dir',
    '.git/config', '.git', '.GIT/config', '%2egit/config', 'site/git-link/config', 'site/git-link',
    'missing.html', 'site/index.html%00.txt', '%E0%A4%A',
  ]) {
    assert.equal(await resolveRepoPath(repo, path), null, path)
  }
})

test('a directory lists its entries without .git', async () => {
  const repo = checkout()
  const { entries } = await listDir(repo)
  assert.deepEqual(entries, [{ name: 'data.bin', type: 'file', size: 1 }, { name: 'site', type: 'dir', size: 0 }])
})
