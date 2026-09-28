// Skills folder for both harnesses: a symlink from each harness's own skills directory
// ($CLAUDE_CONFIG_DIR/skills, $CODEX_HOME/skills) to the project's shared skills folder
// (/project/skills), so a skill dropped in one place is picked up by both engines. Called once at
// server startup.
import { existsSync, lstatSync, mkdirSync, readlinkSync, rmSync, symlinkSync } from 'node:fs'
import { dirname } from 'node:path'

/**
 * Makes each of `targets` a symlink to `skillsDir`. Creates the target's parent directory if
 * missing. A target that is already the right symlink is left alone. A stale symlink (missing, or
 * pointing somewhere else) is replaced. A target that is a real directory (or any other non-symlink
 * file) is left untouched and reported through `log`.
 */
export function linkSkills(skillsDir: string, targets: string[], log: (msg: string) => void = console.log): void {
  for (const target of targets) {
    mkdirSync(dirname(target), { recursive: true })

    if (isSymlink(target)) {
      if (readlinkSync(target) === skillsDir) continue
      rmSync(target)
    } else if (existsSync(target)) {
      log(`skills: ${target} is a real directory, not a symlink to ${skillsDir}; leaving it alone`)
      continue
    }

    symlinkSync(skillsDir, target)
  }
}

function isSymlink(path: string): boolean {
  try {
    return lstatSync(path).isSymbolicLink()
  } catch {
    return false
  }
}
