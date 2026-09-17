import { useEffect, useState } from 'react'
import { ChevronDownIcon, LayoutGridIcon, PlusIcon, RefreshCwIcon } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { cn } from '@/lib/utils'
import { api, type Project, type Session, type WorkerList } from './api'
import type { Activity } from './App'
import { ago, engineName, Menu, MenuItem, MenuSeparator, when, WorkerBadge } from './ui'

const SHOWN_CHATS = 5

export function Sidebar({ email, admin, projects, project, workers, sessions, activity, syncedAt, currentSession, currentWorker, onOverview, onSync, onError, onProjectCreated, onSignOut }: {
  email: string
  admin: boolean
  projects: Project[]
  project?: string
  workers: WorkerList | null
  sessions: Session[]
  activity: Activity
  syncedAt?: Date
  currentSession?: string
  currentWorker?: string
  onOverview: boolean
  onSync: () => void
  onError: (e: unknown) => void
  onProjectCreated: (name: string) => void
  onSignOut: () => void
}) {
  const [creating, setCreating] = useState(false)
  const [expanded, setExpanded] = useState<Record<string, boolean>>({})
  const [, tick] = useState(0)
  useEffect(() => { const t = setInterval(() => tick((n) => n + 1), 30_000); return () => clearInterval(t) }, [])

  // Every worker from git, then any worker that only survives in old chats.
  const groups = (workers?.workers ?? []).map((w) => ({ name: w.name, engine: w.engine, gone: false }))
  for (const s of sessions) {
    if (!groups.some((g) => g.name === s.worker)) groups.push({ name: s.worker, engine: s.engine, gone: true })
  }

  return (
    <aside className="bg-sidebar flex h-full flex-col border-r" aria-label="Projects and workers">
      <div className="flex flex-col gap-1.5 border-b px-3 pt-3.5 pb-2.5">
        <div className="text-faint px-2 text-[13px] font-bold tracking-tight">Bob</div>
        <Menu label="Switch project" triggerClassName="hover:bg-accent flex w-full items-center gap-2 rounded-lg px-2 py-1.5 text-left text-lg font-semibold tracking-tight"
          trigger={<>
            <span className="min-w-0 flex-1 truncate">{project ?? 'Projects'}</span>
            <ChevronDownIcon className="text-faint size-4" />
          </>}>
          {projects.map((p) => (
            <MenuItem key={p.name} checked={p.name === project} onClick={() => { window.location.hash = `/p/${p.name}` }}>{p.name}</MenuItem>
          ))}
          {admin && projects.length > 0 && <MenuSeparator />}
          {admin && <MenuItem checked={false} onClick={() => setCreating(true)}>New project…</MenuItem>}
        </Menu>
        {project && <SyncStatus workers={workers} activity={activity} syncedAt={syncedAt} onSync={onSync} />}
      </div>

      <nav className="flex min-h-0 flex-1 flex-col gap-3.5 overflow-y-auto px-3 pt-2.5 pb-4">
        {project && (
          <a href={`#/p/${project}`} className={cn(
            'text-muted-foreground hover:bg-accent hover:text-foreground flex items-center gap-2.5 rounded-lg px-2 py-1.5 font-medium',
            onOverview && 'bg-muted text-foreground',
          )}>
            <span className="grid w-[22px] place-items-center"><LayoutGridIcon className="size-4" /></span>
            Overview
          </a>
        )}

        {project && workers?.error && <p className="text-destructive px-2 text-xs">{workers.error}</p>}
        {project && workers?.sync.ok && workers.workers.length === 0 && (
          <p className="text-muted-foreground px-2 text-[13px]">No workers yet. Add a <code className="font-mono text-xs">workers/*.md</code> file to the project's folder in git, then sync.</p>
        )}

        {project && groups.map((g) => {
          // Chats with messages first; empty leftovers sink to the bottom (still there to delete).
          const chats = sessions.filter((s) => s.worker === g.name).sort((a, b) => Number(!a.messages) - Number(!b.messages))
          const shown = expanded[g.name] ? chats : chats.slice(0, SHOWN_CHATS)
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
              {chats.length > 0 && (
                <ul className="ml-[19px] flex flex-col">
                  {shown.map((s) => (
                    <li key={s.id}>
                      <a href={`#/p/${project}/s/${s.id}`} className={cn(
                        'text-muted-foreground hover:bg-accent hover:text-foreground flex items-baseline gap-2 rounded-r-md border-l py-[5px] pr-2 pl-3 text-[13.5px]',
                        s.id === currentSession && 'bg-muted text-foreground font-medium',
                      )}>
                        <span className={cn('min-w-0 flex-1 truncate', !s.title && 'italic')}>{s.title || 'Empty chat'}</span>
                        <time className="text-faint shrink-0 text-[11.5px] tabular-nums">{when(s.last_active_at ?? s.created_at)}</time>
                      </a>
                    </li>
                  ))}
                  {chats.length > SHOWN_CHATS && (
                    <li>
                      <button type="button" onClick={() => setExpanded({ ...expanded, [g.name]: !expanded[g.name] })}
                        className="text-faint hover:text-foreground w-full border-l py-1 pr-2 pl-3 text-left text-[12.5px]">
                        {expanded[g.name] ? 'Show fewer' : `Show ${chats.length - SHOWN_CHATS} more`}
                      </button>
                    </li>
                  )}
                </ul>
              )}
            </section>
          )
        })}
      </nav>

      <div className="text-muted-foreground flex items-center gap-2 border-t px-5 py-2.5 text-[12.5px]">
        <span className="min-w-0 flex-1 truncate">{email}</span>
        <button type="button" onClick={onSignOut} className="hover:bg-accent hover:text-foreground rounded px-1 py-0.5">Sign out</button>
      </div>

      <CreateProject open={creating} onOpenChange={setCreating} onError={onError} onCreated={(name) => { setCreating(false); onProjectCreated(name) }} />
    </aside>
  )
}

function SyncStatus({ workers, activity, syncedAt, onSync }: { workers: WorkerList | null; activity: Activity; syncedAt?: Date; onSync: () => void }) {
  const failed = workers && !workers.sync.ok
  const label = activity === 'starting' ? 'Starting the computer…'
    : activity === 'syncing' ? 'Pulling from git…'
    : failed ? 'Git sync failed'
    : syncedAt ? `Synced ${ago(syncedAt)}` : ''
  return (
    <div className="flex items-center gap-2 pl-2 text-[12.5px] whitespace-nowrap">
      <span className={cn('min-w-0 flex-1 truncate', failed ? 'text-destructive' : 'text-muted-foreground')}
        title={failed ? workers.sync.error : workers?.sync.commit ? `At ${workers.sync.commit}` : undefined}>
        {label}
      </span>
      <button type="button" onClick={onSync} disabled={!!activity} aria-label="Sync workers from git" title="Sync workers from git"
        className="text-muted-foreground hover:bg-accent hover:text-foreground grid size-6 place-items-center rounded-md disabled:opacity-60">
        <RefreshCwIcon className={cn('size-3.5', activity && 'animate-spin')} />
      </button>
    </div>
  )
}

function CreateProject({ open, onOpenChange, onCreated, onError }: {
  open: boolean; onOpenChange: (open: boolean) => void; onCreated: (name: string) => void; onError: (e: unknown) => void
}) {
  const empty = { name: '', repo_url: '', repo_ref: 'main', subfolder: '' }
  const [form, setForm] = useState(empty)
  const [busy, setBusy] = useState(false)
  useEffect(() => { if (open) setForm(empty) }, [open]) // eslint-disable-line react-hooks/exhaustive-deps

  const field = (key: keyof typeof form, label: string, placeholder: string, hint?: string) => (
    <label className="flex flex-col gap-1 text-sm">
      <span className="font-medium">{label}</span>
      <Input value={form[key]} placeholder={placeholder} onChange={(e) => setForm({ ...form, [key]: e.target.value })} />
      {hint && <span className="text-muted-foreground text-xs">{hint}</span>}
    </label>
  )
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-lg">
        <form className="flex flex-col gap-4" onSubmit={(e) => {
          e.preventDefault()
          setBusy(true)
          api.createProject(form).then((p) => onCreated(p.name)).catch(onError).finally(() => setBusy(false))
        }}>
          <DialogHeader>
            <DialogTitle>New project</DialogTitle>
            <DialogDescription>A project is a folder in a git repository holding its workers and skills, plus a computer to run them on.</DialogDescription>
          </DialogHeader>
          {field('name', 'Name', 'marketing', 'Lower-case letters, numbers and dashes.')}
          {field('repo_url', 'Repository', 'https://github.com/org/repo')}
          {field('repo_ref', 'Branch', 'main')}
          {field('subfolder', 'Folder', 'e.g. bob', 'Where workers/ and skills/ live. Leave empty for the repository root.')}
          <DialogFooter>
            <Button type="button" variant="ghost" onClick={() => onOpenChange(false)} disabled={busy}>Cancel</Button>
            <Button type="submit" disabled={busy || !form.name || !form.repo_url}>{busy ? 'Creating…' : 'Create project'}</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
