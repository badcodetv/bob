// Read-only access to the project's synced checkout (/project/repo) for Bob's file viewer.
// Anything that would leave the checkout — "..", encoded or not, a symlink pointing out, or the
// .git folder — is reported as not found.
import { createReadStream } from 'node:fs'
import { lstat, readdir, realpath, stat } from 'node:fs/promises'
import { extname, isAbsolute, join, relative, sep } from 'node:path'

export type Resolved =
  | { kind: 'file'; path: string; size: number; type: string }
  | { kind: 'dir'; path: string }

/** Resolves a URL path below /files/ (still percent-encoded) to a file or directory in repoDir. */
export async function resolveRepoPath(repoDir: string, urlPath: string): Promise<Resolved | null> {
  const segments: string[] = []
  for (const raw of urlPath.split('?')[0].split('/')) {
    let seg: string
    try {
      seg = decodeURIComponent(raw)
    } catch {
      return null
    }
    if (seg === '' || seg === '.') continue
    if (seg === '..' || seg.toLowerCase() === '.git' || seg.includes('/') || seg.includes('\\') || seg.includes('\0')) return null
    segments.push(seg)
  }
  let root: string, real: string
  try {
    root = await realpath(repoDir)
    real = await realpath(join(root, ...segments))
  } catch {
    return null
  }
  const rel = relative(root, real)
  if (isAbsolute(rel) || rel.split(sep).some((s) => s === '..' || s.toLowerCase() === '.git')) return null
  const st = await stat(real).catch(() => null)
  if (!st) return null
  if (st.isDirectory()) return { kind: 'dir', path: real }
  if (!st.isFile()) return null
  return { kind: 'file', path: real, size: st.size, type: contentType(real) }
}

/**
 * Lists dir (inside repoDir). A symlink is listed as what it points to when that stays inside the
 * checkout, and left out when it does not, since it could not be opened anyway.
 */
export async function listDir(repoDir: string, dir: string) {
  const entries = []
  const root = await realpath(repoDir)
  for (const name of (await readdir(dir)).sort()) {
    if (name.toLowerCase() === '.git') continue
    let st = await lstat(join(dir, name)).catch(() => null)
    if (st?.isSymbolicLink()) {
      const target = await resolveRepoPath(root, relative(root, join(dir, name)).split(sep).map(encodeURIComponent).join('/'))
      st = target ? await stat(target.path).catch(() => null) : null
    }
    if (!st || !(st.isDirectory() || st.isFile())) continue
    entries.push({ name, type: st.isDirectory() ? 'dir' : 'file', size: st.isFile() ? st.size : 0 })
  }
  return { entries }
}

export function openFile(path: string) {
  return createReadStream(path)
}

const types: Record<string, string> = {
  '.html': 'text/html; charset=utf-8', '.htm': 'text/html; charset=utf-8',
  '.css': 'text/css; charset=utf-8', '.js': 'text/javascript; charset=utf-8', '.mjs': 'text/javascript; charset=utf-8',
  '.json': 'application/json', '.jsonl': 'application/jsonl; charset=utf-8', '.xml': 'application/xml',
  '.txt': 'text/plain; charset=utf-8', '.md': 'text/markdown; charset=utf-8', '.csv': 'text/csv; charset=utf-8',
  '.yaml': 'text/yaml; charset=utf-8', '.yml': 'text/yaml; charset=utf-8', '.log': 'text/plain; charset=utf-8',
  '.svg': 'image/svg+xml', '.png': 'image/png', '.jpg': 'image/jpeg', '.jpeg': 'image/jpeg', '.gif': 'image/gif',
  '.webp': 'image/webp', '.ico': 'image/x-icon', '.avif': 'image/avif',
  '.pdf': 'application/pdf', '.woff': 'font/woff', '.woff2': 'font/woff2', '.ttf': 'font/ttf', '.otf': 'font/otf',
  '.mp4': 'video/mp4', '.webm': 'video/webm', '.mp3': 'audio/mpeg', '.wav': 'audio/wav',
}

export function contentType(path: string): string {
  return types[extname(path).toLowerCase()] ?? 'application/octet-stream'
}
