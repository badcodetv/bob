import { useEffect, useMemo, useState, type ReactNode } from 'react'
import { ChevronDownIcon, ClockIcon, FolderOpenIcon, LayoutGridIcon, PlusIcon, UsersIcon } from 'lucide-react'
import { cn } from '@/lib/utils'
import type { Project, Session, Worker } from './api'
import type { Page } from './App'
import { engineName, Menu, MenuItem, when, WorkerBadge } from './ui'

const SHOWN_CHATS = 5

/** One requirement of a parsed label selector (mirrors the server's labels package). */
type Requirement =
  | { op: 'equals' | 'notEquals'; key: string; value: string }
  | { op: 'in' | 'notIn'; key: string; values: string[] }
  | { op: 'exists' | 'notExists'; key: string }

/**
 * Parses a label selector: `k=v`, `k!=v`, `k in (a,b)`, `k notin (a)`, `k` / `exists k`, `!k`,
 * comma-separated terms ANDed together. Throws a readable Error on a malformed selector — the
 * same grammar as `api/internal/labels`, evaluated client-side against a worker's labels so the
 * sidebar's list can filter without a round trip.
 */
function parseSelector(s: string): Requirement[] {
  const trimmed = s.trim()
  if (!trimmed) return []
  const terms = splitTopLevel(trimmed)
  return terms.map(parseTerm)
}

function splitTopLevel(s: string): string[] {
  const terms: string[] = []
  let depth = 0, start = 0
  for (let i = 0; i < s.length; i++) {
    const c = s[i]
    if (c === '(') depth++
    else if (c === ')') { depth--; if (depth < 0) throw new Error('selector: unmatched )') }
    else if (c === ',' && depth === 0) { terms.push(s.slice(start, i)); start = i + 1 }
  }
  if (depth !== 0) throw new Error('selector: unterminated (')
  terms.push(s.slice(start))
  const trimmedTerms = terms.map((t) => t.trim())
  if (trimmedTerms.some((t) => !t)) throw new Error('selector: empty term')
  return trimmedTerms
}

function parseTerm(term: string): Requirement {
  let m: RegExpMatchArray | null
  if ((m = term.match(/^!\s*(\S+)$/))) return { op: 'notExists', key: m[1] }
  if ((m = term.match(/^exists\s+(\S+)$/))) return { op: 'exists', key: m[1] }
  if ((m = term.match(/^(\S+)\s+in\s*\(([^)]*)\)$/))) return { op: 'in', key: m[1], values: splitValues(m[2]) }
  if ((m = term.match(/^(\S+)\s+notin\s*\(([^)]*)\)$/))) return { op: 'notIn', key: m[1], values: splitValues(m[2]) }
  if ((m = term.match(/^([^!=\s]+)\s*!=\s*(\S+)$/))) return { op: 'notEquals', key: m[1], value: m[2] }
  if ((m = term.match(/^([^!=\s]+)\s*=\s*(\S+)$/))) return { op: 'equals', key: m[1], value: m[2] }
  if (!/\s/.test(term)) return { op: 'exists', key: term }
  throw new Error(`selector: could not parse "${term}"`)
}

function splitValues(s: string): string[] {
  const vals = s.split(',').map((v) => v.trim()).filter(Boolean)
  if (vals.length === 0) throw new Error('selector: ( ) needs at least one value')
  return vals
}

function matchesSelector(labels: Record<string, string>, reqs: Requirement[]): boolean {
  return reqs.every((r) => {
    switch (r.op) {
      case 'equals': return labels[r.key] === r.value
      case 'notEquals': return labels[r.key] !== r.value
      case 'in': return r.values.includes(labels[r.key])
      case 'notIn': return !r.values.includes(labels[r.key])
      case 'exists': return Object.hasOwn(labels, r.key)
      case 'notExists': return !Object.hasOwn(labels, r.key)
    }
  })
}

export function Sidebar({ email, projects, project, workers, sessions, currentSession, currentWorker, page, attention, onSignOut }: {
  email: string
  projects: Project[]
  project?: string
  workers: Worker[] | null
  sessions: Session[]
  currentSession?: string
  currentWorker?: string
  page: Page
  /** How many open requests for a person the project has. */
  attention: number
  onSignOut: () => void
}) {
  const [expanded, setExpanded] = useState<Record<string, boolean>>({})
  const [, tick] = useState(0)
  useEffect(() => { const t = setInterval(() => tick((n) => n + 1), 30_000); return () => clearInterval(t) }, [])

  // A selector box above the worker groups filters them by label, client-side (the labels are
  // already in `workers`). An unparsable selector is shown as an error and does not filter.
  const [selectorText, setSelectorText] = useState('')
  const { selectorReqs, selectorError } = useMemo(() => {
    try {
      return { selectorReqs: parseSelector(selectorText), selectorError: null as string | null }
    } catch (e) {
      return { selectorReqs: null, selectorError: (e as Error).message }
    }
  }, [selectorText])

  // Every current worker, then any worker that only survives in old chats. Plain chats
  // (worker === '') are not a worker at all — they get their own "Chat" group below, not a
  // "removed worker" group. A removed worker's labels are gone with it, so a selector always
  // excludes those groups.
  const workerLabels = new Map((workers ?? []).map((w) => [w.name, w.labels]))
  let groups = (workers ?? []).map((w) => ({ name: w.name, engine: w.engine, gone: false }))
  for (const s of sessions) {
    if (s.worker !== '' && !groups.some((g) => g.name === s.worker)) groups.push({ name: s.worker, engine: s.engine, gone: true })
  }
  if (selectorReqs && selectorReqs.length > 0) {
    groups = groups.filter((g) => !g.gone && matchesSelector(workerLabels.get(g.name) ?? {}, selectorReqs!))
  }
  const plainChats = sessions.filter((s) => s.worker === '').sort((a, b) => Number(!a.messages) - Number(!b.messages))

  return (
    <aside className="bg-sidebar flex h-full flex-col border-r" aria-label="Projects and workers">
      <div className="flex flex-col gap-1.5 border-b px-3 pt-3.5 pb-2.5">
        <div className="text-faint px-2 text-[13px] font-bold tracking-tight">Bob</div>
        <Menu label="Switch project" triggerClassName="hover:bg-accent flex w-full items-center gap-2 rounded-lg px-2 py-1.5 text-left text-lg font-semibold tracking-tight"
          trigger={<>
            <span className="min-w-0 flex-1 truncate">{project ?? 'Projects'}</span>
            {attention > 0 && (
              <span title={`${attention} need${attention === 1 ? 's' : ''} you`} aria-label={`${attention} chats need you`}
                className="bg-destructive text-destructive-foreground rounded-full px-1.5 py-px text-[11px] leading-4 font-semibold tabular-nums">{attention}</span>
            )}
            <ChevronDownIcon className="text-faint size-4" />
          </>}>
          {projects.map((p) => (
            <MenuItem key={p.name} checked={p.name === project} onClick={() => { window.location.hash = `/p/${p.name}` }}>{p.name}</MenuItem>
          ))}
        </Menu>
      </div>

      <nav className="flex min-h-0 flex-1 flex-col gap-3.5 overflow-y-auto px-3 pt-2.5 pb-4">
        {project && (
          <div className="flex flex-col gap-px">
            <NavLink href={`#/p/${project}`} active={page === 'overview'} icon={<LayoutGridIcon className="size-4" />}>Overview</NavLink>
            <NavLink href={`#/p/${project}/workers`} active={page === 'workers'} icon={<UsersIcon className="size-4" />}>Workers</NavLink>
            <NavLink href={`#/p/${project}/files`} active={page === 'files'} icon={<FolderOpenIcon className="size-4" />}>Files</NavLink>
            <NavLink href={`#/p/${project}/schedules`} active={page === 'schedules'} icon={<ClockIcon className="size-4" />}>Schedules</NavLink>
          </div>
        )}

        {project && workers && workers.length === 0 && (
          <p className="text-muted-foreground px-2 text-[13px]">
            No workers yet. <a href={`#/p/${project}/workers/new`} className="underline underline-offset-2">Create one</a> to start chatting.
          </p>
        )}

        {project && (plainChats.length > 0 || workers) && (
          <section className="flex flex-col gap-px">
            <div className="flex items-center gap-2.5 py-1 pr-1 pl-2">
              <span className="truncate text-[14.5px] font-semibold">Chat</span>
              <a href={`#/p/${project}/new/`} title="New plain chat" aria-label="New plain chat"
                className={cn('text-muted-foreground hover:bg-accent hover:text-foreground ml-auto grid size-[26px] place-items-center rounded-md',
                  currentWorker === '' && 'bg-muted text-foreground')}>
                <PlusIcon className="size-4" />
              </a>
            </div>
            <ChatList groupKey="__plain" chats={plainChats} project={project} currentSession={currentSession}
              expanded={expanded} setExpanded={setExpanded} />
          </section>
        )}

        {project && workers && workers.length > 0 && (
          <div className="flex flex-col gap-1 px-2">
            <input value={selectorText} onChange={(e) => setSelectorText(e.target.value)}
              placeholder="Filter workers by label, e.g. kind=hypothesis"
              aria-label="Filter workers by label selector"
              className="border-input placeholder:text-faint h-7 rounded-md border bg-transparent px-2 text-[12.5px]" />
            {selectorError && <span className="text-destructive text-[11.5px]">{selectorError}</span>}
          </div>
        )}

        {project && groups.map((g) => {
          // Chats with messages first; empty leftovers sink to the bottom (still there to delete).
          const chats = sessions.filter((s) => s.worker === g.name).sort((a, b) => Number(!a.messages) - Number(!b.messages))
          return (
            <section key={g.name} className="flex flex-col gap-px">
              <div className="flex items-center gap-2.5 py-1 pr-1 pl-2">
                <WorkerBadge name={g.name} engine={g.engine} />
                <span className="truncate text-[14.5px] font-semibold">{g.name}</span>
                <span className="text-faint text-xs">{g.gone ? 'removed' : engineName(g.engine)}</span>
                {!g.gone && (
                  <a href={`#/p/${project}/new/${g.name}`} title={`New chat with ${g.name}`} aria-label={`New chat with ${g.name}`}
                    className={cn('text-muted-foreground hover:bg-accent hover:text-foreground ml-auto grid size-[26px] place-items-center rounded-md',
                      currentWorker === g.name && 'bg-muted text-foreground')}>
                    <PlusIcon className="size-4" />
                  </a>
                )}
              </div>
              <ChatList groupKey={g.name} chats={chats} project={project} currentSession={currentSession}
                expanded={expanded} setExpanded={setExpanded} />
            </section>
          )
        })}
      </nav>

      <div className="text-muted-foreground flex items-center gap-2 border-t px-5 py-2.5 text-[12.5px]">
        <span className="min-w-0 flex-1 truncate">{email}</span>
        <button type="button" onClick={onSignOut} className="hover:bg-accent hover:text-foreground rounded px-1 py-0.5">Sign out</button>
      </div>

    </aside>
  )
}

/** A group's chat list: the first SHOWN_CHATS, with a "show more" toggle kept per group. */
function ChatList({ groupKey, chats, project, currentSession, expanded, setExpanded }: {
  groupKey: string; chats: Session[]; project: string; currentSession?: string
  expanded: Record<string, boolean>; setExpanded: (e: Record<string, boolean>) => void
}) {
  if (chats.length === 0) return null
  const shown = expanded[groupKey] ? chats : chats.slice(0, SHOWN_CHATS)
  return (
    <ul className="ml-[19px] flex flex-col">
      {shown.map((s) => (
        <li key={s.id}>
          <a href={`#/p/${project}/s/${s.id}`} className={cn(
            'text-muted-foreground hover:bg-accent hover:text-foreground flex items-baseline gap-2 rounded-r-md border-l py-[5px] pr-2 pl-3 text-[13.5px]',
            s.id === currentSession && 'bg-muted text-foreground font-medium',
          )}>
            {s.schedule
              ? <span className="flex min-w-0 flex-1 items-center gap-1.5 truncate" title={s.title}><ClockIcon className="text-faint size-3 shrink-0" />{s.schedule}</span>
              : <span className={cn('min-w-0 flex-1 truncate', !s.title && 'italic')}>{s.title || 'Empty chat'}</span>}
            {s.attention && <span title="Needs you" aria-label="Needs you" className="bg-destructive size-2 shrink-0 self-center rounded-full" />}
            <time className="text-faint shrink-0 text-[11.5px] tabular-nums">{when(s.last_active_at ?? s.created_at)}</time>
          </a>
        </li>
      ))}
      {chats.length > SHOWN_CHATS && (
        <li>
          <button type="button" onClick={() => setExpanded({ ...expanded, [groupKey]: !expanded[groupKey] })}
            className="text-faint hover:text-foreground w-full border-l py-1 pr-2 pl-3 text-left text-[12.5px]">
            {expanded[groupKey] ? 'Show fewer' : `Show ${chats.length - SHOWN_CHATS} more`}
          </button>
        </li>
      )}
    </ul>
  )
}

function NavLink({ href, active, icon, children }: { href: string; active: boolean; icon: ReactNode; children: ReactNode }) {
  return (
    <a href={href} className={cn(
      'text-muted-foreground hover:bg-accent hover:text-foreground flex items-center gap-2.5 rounded-lg px-2 py-1.5 font-medium',
      active && 'bg-muted text-foreground',
    )}>
      <span className="grid w-[22px] place-items-center">{icon}</span>
      {children}
    </a>
  )
}
