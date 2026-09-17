import { timingSafeEqual, createHash } from 'node:crypto'

/** Whether an Authorization header is basic auth with password token (any user name). */
export function checkAuth(header: string | undefined, token: string): boolean {
  if (!token || !header?.startsWith('Basic ')) return false
  const decoded = Buffer.from(header.slice(6), 'base64').toString('utf8')
  const password = decoded.slice(decoded.indexOf(':') + 1)
  if (decoded.indexOf(':') < 0) return false
  // Compare digests, so the comparison takes the same time whatever the lengths.
  const digest = (s: string) => createHash('sha256').update(s).digest()
  return timingSafeEqual(digest(password), digest(token))
}
