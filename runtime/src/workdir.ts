// Each session works in its own git worktree of the project's repository: a separate checkout
// on its own branch, so chats can read and change the code without stepping on each other.
import { execFile } from 'node:child_process'
import { mkdir, rm, stat } from 'node:fs/promises'
import { join } from 'node:path'
import { promisify } from 'node:util'

const run = promisify(execFile)

export function branchFor(sessionId: string): string {
  return `bob/${sessionId}`
}

/**
 * Returns the session's working directory, creating it on first use: a worktree of repoDir at
 * its current commit when the project has a repository, otherwise an empty folder. A directory
 * that already exists is used as it is, so sessions from before worktrees keep their folders.
 */
export async function prepareWorkdir(projectDir: string, repoDir: string, sessionId: string): Promise<string> {
  const dir = join(projectDir, 'work', sessionId)
  if (await exists(dir)) return dir
  await mkdir(join(projectDir, 'work'), { recursive: true })
  if (!(await exists(join(repoDir, '.git')))) {
    await mkdir(dir, { recursive: true })
    return dir
  }
  await run('git', ['-C', repoDir, 'worktree', 'prune'])
  await run('git', ['-C', repoDir, 'worktree', 'add', '--quiet', '-b', branchFor(sessionId), dir, 'HEAD'])
  return dir
}

async function exists(path: string): Promise<boolean> {
  return stat(path).then(() => true, () => false)
}

/** Removes a session's working directory, and its worktree branch when it has one. */
export async function removeWorkdir(projectDir: string, repoDir: string, sessionId: string): Promise<void> {
  const dir = join(projectDir, 'work', sessionId)
  if (await exists(join(repoDir, '.git'))) {
    await run('git', ['-C', repoDir, 'worktree', 'remove', '--force', dir]).catch(() => {})
    await run('git', ['-C', repoDir, 'branch', '-D', branchFor(sessionId)]).catch(() => {})
  }
  await rm(dir, { recursive: true, force: true })
}
