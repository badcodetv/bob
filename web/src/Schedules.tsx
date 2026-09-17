import { useCallback, useEffect, useRef, useState, type ReactNode } from 'react'
import { PauseIcon, PlayIcon, PlusIcon } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { cn } from '@/lib/utils'
import { api, type ScheduleInput, type ScheduleRun, type ScheduleView, type WorkerList } from './api'
import { describeCron } from './cron'
import { Empty, Section } from './Overview'
import { PageBar, WorkerBadge } from './ui'

/** A project's schedules: each starts a new chat on a worker with a fixed message when its cron fires. */
export function Schedules({ project, admin, workers, onRan, onError }: {
  project: string
  admin: boolean
  workers: WorkerList | null
  onRan: () => void
  onError: (e: unknown) => void
}) {
  const [list, setList] = useState<ScheduleView[] | null>(null)
  const [paused, setPaused] = useState(false)
  const [editing, setEditing] = useState<ScheduleView | 'new' | null>(null)

  const load = useCallback(() => api.schedules(project).then((r) => { setList(r.schedules); setPaused(r.paused) }).catch(onError), [project, onError])
  useEffect(() => { load() }, [load])
  // Refresh often while a run is going, otherwise once a minute (next firings move on).
  const running = list?.some((s) => s.last_run?.status === 'running')
  useEffect(() => {
    const t = setInterval(load, running ? 5_000 : 60_000)
    return () => clearInterval(t)
  }, [load, running])
  // When a run finishes, its chat has news: let the sidebar reload.
  const wasRunning = useRef(false)
  useEffect(() => {
    if (wasRunning.current && !running) onRan()
    wasRunning.current = !!running
  }, [running, onRan])

  const togglePause = () => api.updateSettings({ schedules_paused: !paused }).then((r) => setPaused(r.schedules_paused)).catch(onError)
  const runNow = (s: ScheduleView) => api.runSchedule(s.id).then(() => { load(); onRan() }).catch(onError)
  const setEnabled = (s: ScheduleView, enabled: boolean) => api.updateSchedule(s.id, { enabled }).then(load).catch(onError)

  return (
    <div className="flex h-full flex-col">
      <PageBar>
        <span className="text-[15px] font-semibold">Schedules</span>
        <span className="flex-1" />
        {admin && (
          <Button variant="outline" size="sm" onClick={togglePause} title={paused ? 'Let every schedule fire again' : 'Stop every schedule in every project from firing'}>
            {paused ? <PlayIcon /> : <PauseIcon />}
            {paused ? 'Resume all schedules' : 'Pause all schedules'}
          </Button>
        )}
        {admin && <Button size="sm" onClick={() => setEditing('new')}><PlusIcon />New schedule</Button>}
      </PageBar>

      <div className="min-h-0 flex-1 overflow-y-auto">
        <div className="mx-auto flex max-w-[880px] flex-col gap-6 px-4 pt-6 pb-16 md:px-10 md:pt-9">
          {paused && (
            <p className="border-warn/40 text-warn rounded-lg border px-3 py-2.5 text-sm">
              All schedules are paused, in every project. Nothing fires until an admin resumes them; <b>Run now</b> still works.
            </p>
          )}
          <Section title="Schedules" count={list?.length}>
            {!list ? <Empty>Loading…</Empty>
              : list.length === 0 ? (
                <Empty>
                  No schedules yet. A schedule starts a new chat with a worker, with the same first message, at the times you set.
                  {!admin && ' Ask an admin to add one.'}
                </Empty>
              ) : (
                <ul>
                  {list.map((s) => (
                    <ScheduleRow key={s.id} project={project} schedule={s} engine={workers?.workers.find((w) => w.name === s.worker)?.engine ?? ''} admin={admin} paused={paused}
                      onRunNow={() => runNow(s)} onEnabled={(on) => setEnabled(s, on)} onEdit={() => setEditing(s)} onError={onError} />
                  ))}
                </ul>
              )}
          </Section>
        </div>
      </div>

      <ScheduleEditor project={project} schedule={editing} workers={workers} onClose={() => setEditing(null)}
        onSaved={() => { setEditing(null); load() }} onError={onError} />
    </div>
  )
}

function ScheduleRow({ project, schedule: s, engine, admin, paused, onRunNow, onEnabled, onEdit, onError }: {
  project: string; schedule: ScheduleView; engine: string; admin: boolean; paused: boolean
  onRunNow: () => void; onEnabled: (on: boolean) => void; onEdit: () => void; onError: (e: unknown) => void
}) {
  const [history, setHistory] = useState<ScheduleRun[] | null>(null)
  const [open, setOpen] = useState(false)
  const lastRunKey = s.last_run ? `${s.last_run.id}:${s.last_run.status}` : ''
  useEffect(() => {
    if (open) api.scheduleRuns(s.id).then(setHistory).catch(onError)
  }, [open, s.id, lastRunKey, onError])
  const running = s.last_run?.status === 'running'

  return (
    <li className="flex flex-col gap-2 border-t px-1 py-4 first:border-t-0">
      <div className="flex flex-wrap items-center gap-2.5">
        <span className="text-[15px] font-semibold">{s.name}</span>
        <span className="text-faint flex items-center gap-1.5 text-[13px]">
          <WorkerBadge name={s.worker} engine={engine} /> {s.worker}
        </span>
        {!s.enabled && <span className="bg-muted text-muted-foreground rounded px-1.5 py-0.5 text-xs">Off</span>}
        <span className="flex-1" />
        {admin && (
          <Button variant="ghost" size="sm" onClick={() => onEnabled(!s.enabled)}>{s.enabled ? 'Turn off' : 'Turn on'}</Button>
        )}
        {admin && <Button variant="ghost" size="sm" onClick={onEdit}>Edit</Button>}
        <Button variant="outline" size="sm" onClick={onRunNow} disabled={running}>{running ? 'Running…' : 'Run now'}</Button>
      </div>

      <div className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-1.5 text-[13.5px]">
        <Label>When</Label>
        <span>{describeCron(s.cron, s.timezone)} <code className="text-faint font-mono text-xs">{s.cron}</code></span>
        <Label>Next</Label>
        <span className={cn(!s.enabled || paused ? 'text-faint' : '')}>
          {!s.enabled ? 'Off' : s.next_at ? `${fullTime(s.next_at, s.timezone)}${paused ? ' (paused)' : ''}` : '—'}
        </span>
        <Label>Last run</Label>
        <span>{s.last_run ? <RunLine project={project} run={s.last_run} /> : <span className="text-faint">Never</span>}</span>
        <Label>Message</Label>
        <span className="text-muted-foreground line-clamp-2" title={s.message}>{s.message}</span>
      </div>

      <button type="button" onClick={() => setOpen(!open)} className="text-faint hover:text-foreground self-start text-[12.5px] underline underline-offset-2">
        {open ? 'Hide history' : 'History'}
      </button>
      {open && (
        <ul className="flex flex-col gap-1 border-l pl-3 text-[13px]">
          {!history ? <li className="text-faint">Loading…</li>
            : history.length === 0 ? <li className="text-faint">No runs yet.</li>
            : history.map((r) => <li key={r.id}><RunLine project={project} run={r} /></li>)}
        </ul>
      )}
    </li>
  )
}

function Label({ children }: { children: ReactNode }) {
  return <span className="text-faint pt-px text-[12.5px]">{children}</span>
}

const statusStyle: Record<ScheduleRun['status'], string> = {
  running: 'text-cobalt',
  ok: 'text-foreground',
  failed: 'text-destructive',
  skipped: 'text-warn',
}
const statusWord: Record<ScheduleRun['status'], string> = { running: 'Running', ok: 'Done', failed: 'Failed', skipped: 'Skipped' }

function RunLine({ project, run }: { project: string; run: ScheduleRun }) {
  return (
    <span className="inline-flex flex-wrap items-baseline gap-x-2">
      <span className={cn('font-medium', statusStyle[run.status], run.status === 'running' && 'animate-pulse')}>{statusWord[run.status]}</span>
      <span className="text-faint">{fullTime(run.started_at)}{run.trigger === 'manual' ? ', run by hand' : ''}</span>
      {run.detail && <span className="text-muted-foreground [overflow-wrap:anywhere]">{run.detail}</span>}
      {run.session_id && <a href={`#/p/${project}/s/${run.session_id}`} className="underline underline-offset-2">Open chat</a>}
    </span>
  )
}

/** A date and time in the viewer's timezone, or in timeZone (then named) when given. */
function fullTime(iso: string, timeZone?: string) {
  const text = new Date(iso).toLocaleString([], { weekday: 'short', day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit', hourCycle: 'h23', timeZone })
  return timeZone ? `${text} (${timeZone})` : text
}

const localZone = Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'

function ScheduleEditor({ project, schedule, workers, onClose, onSaved, onError }: {
  project: string; schedule: ScheduleView | 'new' | null; workers: WorkerList | null
  onClose: () => void; onSaved: () => void; onError: (e: unknown) => void
}) {
  const blank: ScheduleInput = { name: '', worker: workers?.workers[0]?.name ?? '', cron: '0 6 * * *', timezone: localZone, message: '', enabled: true, keep_sessions: 30 }
  const [form, setForm] = useState<ScheduleInput>(blank)
  const [busy, setBusy] = useState(false)
  const [confirmDelete, setConfirmDelete] = useState(false)
  const existing = schedule && schedule !== 'new' ? schedule : null
  useEffect(() => {
    setConfirmDelete(false)
    if (schedule === 'new') setForm(blank)
    else if (schedule) setForm({ name: schedule.name, worker: schedule.worker, cron: schedule.cron, timezone: schedule.timezone, message: schedule.message, enabled: schedule.enabled, keep_sessions: schedule.keep_sessions })
  }, [schedule]) // eslint-disable-line react-hooks/exhaustive-deps

  const set = <K extends keyof ScheduleInput>(k: K, v: ScheduleInput[K]) => setForm({ ...form, [k]: v })
  const save = () => {
    setBusy(true)
    const done = existing ? api.updateSchedule(existing.id, form) : api.createSchedule(project, form)
    done.then(onSaved).catch(onError).finally(() => setBusy(false))
  }
  const remove = () => {
    if (!existing) return
    setBusy(true)
    api.deleteSchedule(existing.id).then(onSaved).catch(onError).finally(() => setBusy(false))
  }
  const words = describeCron(form.cron, form.timezone)
  const workerNames = workers?.workers.map((w) => w.name) ?? []
  if (form.worker && !workerNames.includes(form.worker)) workerNames.push(form.worker)

  return (
    <Dialog open={schedule !== null} onOpenChange={(o) => { if (!o) onClose() }}>
      <DialogContent className="sm:max-w-lg">
        <form className="flex flex-col gap-4" onSubmit={(e) => { e.preventDefault(); save() }}>
          <DialogHeader>
            <DialogTitle>{existing ? `Edit ${existing.name}` : 'New schedule'}</DialogTitle>
            <DialogDescription>Each time it fires, Bob pulls from git and starts a new chat with the worker, sending this message.</DialogDescription>
          </DialogHeader>
          <Field label="Name" hint="Lower-case letters, numbers and dashes.">
            <Input value={form.name} placeholder="daily-research" onChange={(e) => set('name', e.target.value)} />
          </Field>
          <Field label="Worker">
            <select value={form.worker} onChange={(e) => set('worker', e.target.value)}
              className="border-input h-8 rounded-lg border bg-transparent px-2 text-sm">
              {workerNames.length === 0 && <option value="">No workers</option>}
              {workerNames.map((n) => <option key={n} value={n}>{n}</option>)}
            </select>
          </Field>
          <div className="grid grid-cols-[minmax(0,1fr)_minmax(0,1fr)] gap-3">
            <Field label="Cron" hint="minute hour day month weekday">
              <Input value={form.cron} className="font-mono" onChange={(e) => set('cron', e.target.value)} />
            </Field>
            <Field label="Timezone">
              <Input value={form.timezone} placeholder="Europe/London" onChange={(e) => set('timezone', e.target.value)} />
            </Field>
          </div>
          <p className="text-muted-foreground -mt-2 text-[13px]">{words === form.cron ? 'Bob will check this expression when you save.' : words}</p>
          <Field label="Message" hint="The first message of every chat this schedule starts.">
            <Textarea value={form.message} rows={4} onChange={(e) => set('message', e.target.value)} />
          </Field>
          <div className="grid grid-cols-[minmax(0,1fr)_minmax(0,1fr)] items-end gap-3">
            <Field label="Keep chats" hint="Older chats from this schedule are deleted.">
              <Input type="number" min={1} value={form.keep_sessions} onChange={(e) => set('keep_sessions', Number(e.target.value))} />
            </Field>
            <label className="flex items-center gap-2 pb-6 text-sm">
              <input type="checkbox" checked={form.enabled} onChange={(e) => set('enabled', e.target.checked)} />
              On
            </label>
          </div>
          <DialogFooter>
            {existing && (confirmDelete
              ? <Button type="button" variant="destructive" onClick={remove} disabled={busy} className="mr-auto">Delete it (its chats stay)</Button>
              : <Button type="button" variant="ghost" onClick={() => setConfirmDelete(true)} disabled={busy} className="text-destructive mr-auto">Delete</Button>)}
            <Button type="button" variant="ghost" onClick={onClose} disabled={busy}>Cancel</Button>
            <Button type="submit" disabled={busy || !form.name || !form.worker || !form.message.trim()}>{busy ? 'Saving…' : 'Save'}</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

function Field({ label, hint, children }: { label: string; hint?: string; children: ReactNode }) {
  return (
    <label className="flex flex-col gap-1 text-sm">
      <span className="font-medium">{label}</span>
      {children}
      {hint && <span className="text-muted-foreground text-xs">{hint}</span>}
    </label>
  )
}
