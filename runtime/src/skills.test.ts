import { test } from 'node:test'
import assert from 'node:assert/strict'
import { mkdtempSync, symlinkSync, mkdirSync, lstatSync, readlinkSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { linkSkills } from './skills.js'

function tmp(): string {
  return mkdtempSync(join(tmpdir(), 'bob-skills-'))
}

test('creates a symlink from each target to skillsDir, making the parent dir if missing', () => {
  const root = tmp()
  const skillsDir = join(root, 'project', 'skills')
  mkdirSync(skillsDir, { recursive: true })
  const target = join(root, 'nested', 'deep', 'claude', 'skills')

  linkSkills(skillsDir, [target])

  const st = lstatSync(target)
  assert.ok(st.isSymbolicLink())
  assert.equal(readlinkSync(target), skillsDir)
})

test('replaces a stale symlink pointing elsewhere', () => {
  const root = tmp()
  const skillsDir = join(root, 'project', 'skills')
  mkdirSync(skillsDir, { recursive: true })
  const target = join(root, 'claude', 'skills')
  mkdirSync(join(root, 'claude'), { recursive: true })
  const elsewhere = join(root, 'elsewhere')
  mkdirSync(elsewhere, { recursive: true })
  symlinkSync(elsewhere, target)

  linkSkills(skillsDir, [target])

  assert.equal(readlinkSync(target), skillsDir)
})

test('leaves a real directory alone and logs it, without touching it', () => {
  const root = tmp()
  const skillsDir = join(root, 'project', 'skills')
  mkdirSync(skillsDir, { recursive: true })
  const target = join(root, 'claude', 'skills')
  mkdirSync(target, { recursive: true })

  const logged: string[] = []
  linkSkills(skillsDir, [target], (msg: string) => logged.push(msg))

  const st = lstatSync(target)
  assert.ok(st.isDirectory())
  assert.ok(!st.isSymbolicLink())
  assert.ok(logged.some((m) => m.includes(target)))
})

test('an already-correct symlink is left as is', () => {
  const root = tmp()
  const skillsDir = join(root, 'project', 'skills')
  mkdirSync(skillsDir, { recursive: true })
  const target = join(root, 'claude', 'skills')
  mkdirSync(join(root, 'claude'), { recursive: true })
  symlinkSync(skillsDir, target)

  linkSkills(skillsDir, [target])

  assert.equal(readlinkSync(target), skillsDir)
})

test('handles multiple targets independently', () => {
  const root = tmp()
  const skillsDir = join(root, 'project', 'skills')
  mkdirSync(skillsDir, { recursive: true })
  const t1 = join(root, 'a', 'skills')
  const t2 = join(root, 'b', 'skills')

  linkSkills(skillsDir, [t1, t2])

  assert.equal(readlinkSync(t1), skillsDir)
  assert.equal(readlinkSync(t2), skillsDir)
})
