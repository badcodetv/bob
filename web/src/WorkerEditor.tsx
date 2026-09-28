import { useCallback, useEffect, useState, type ReactNode } from 'react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { api, type Worker, type WorkerVersion } from './api'
import { claudeEfforts, claudeModels } from './engines/claude'
import { Empty, Section } from './Overview'
import { PageBar, when, WorkerBadge } from './ui'

// codex's effort levels (workers.go's codexEfforts); there is no client-side codex converter yet,
// so unlike claude there is no model list to offer — model stays a free-text field for both.
const codexEfforts = ['minimal', 'low', 'medium', 'high', 'xhigh']
const effortsFor = (engine: string) => (engine === 'codex' ? codexEfforts : claudeEfforts)

type Form = { name: string; engine: string; model: string; effort: string; tools: string; prompt: string }
const blank = (w?: Worker): Form => ({
  name: w?.name ?? '', engine: w?.engine ?? 'claude', model: w?.model ?? '', effort: w?.effort ?? '',
  tools: (w?.tools ?? []).join(', '), prompt: w?.prompt ?? '',
})

/**
 * Create a worker (name is undefined) or edit an existing one, with its version history.
 * `workers` is the project's already-loaded list; it supplies the existing worker's current
 * values without another round trip.
 */
export function WorkerEditor({ project, name, workers, onSaved, onDeleted, onError }: {
  project: string
  name?: string
  workers: Worker[] | null
  onSaved: () => void
  onDeleted: () => void
  onError: (e: unknown) => void
}) {
  const existing = name ? workers?.find((w) => w.name === name) : undefined
  const [form, setForm] = useState<Form>(() => blank(existing))
  // Reset the form once the real worker arrives (it may still be loading on first render).
  useEffect(() => { if (existing) setForm(blank(existing)) }, [existing?.id]) // eslint-disable-line react-hooks/exhaustive-deps
  const [why, setWhy] = useState('')
  const [busy, setBusy] = useState(false)
  const [confirmDelete, setConfirmDelete] = useState(false)
  const [deleteWhy, setDeleteWhy] = useState('')
  const [versions, setVersions] = useState<WorkerVersion[] | null>(null)

  const loadVersions = useCallback(() => {
    if (name) api.workerVersions(project, name).then(setVersions).catch(onError)
  }, [project, name, onError])
  useEffect(() => { setVersions(null); loadVersions() }, [loadVersions])

  const set = <K extends keyof Form>(k: K, v: Form[K]) => setForm({ ...form, [k]: v })
  const changeEngine = (engine: string) => setForm({ ...form, engine, effort: '', tools: engine === 'claude' ? form.tools : '' })

  const save = () => {
    if (!why.trim() || (!name && !form.name.trim())) return
    setBusy(true)
    const tools = form.engine === 'claude' && form.tools.trim() ? form.tools.split(',').map((t) => t.trim()).filter(Boolean) : undefined
    const body = { engine: form.engine, model: form.model, effort: form.effort, tools, prompt: form.prompt, why: why.trim() }
    const done = name ? api.updateWorker(project, name, body) : api.createWorker(project, { ...body, name: form.name.trim() })
    done.then(onSaved).catch(onError).finally(() => setBusy(false))
  }

  const remove = () => {
    if (!name || !deleteWhy.trim()) return
    setBusy(true)
    api.deleteWorker(project, name, deleteWhy.trim()).then(onDeleted).catch(onError).finally(() => setBusy(false))
  }

  return (
    <div className="flex h-full flex-col">
      <PageBar>
        <WorkerBadge name={form.name || '?'} engine={form.engine} />
        <span className="text-[15px] font-semibold">{name ? `Edit ${name}` : 'New worker'}</span>
        <span className="flex-1" />
        <Button variant="ghost" size="sm" nativeButton={false} render={<a href={`#/p/${project}/workers`} />}>Cancel</Button>
      </PageBar>

      <div className="min-h-0 flex-1 overflow-y-auto">
        <div className="mx-auto flex max-w-[720px] flex-col gap-8 px-4 pt-6 pb-16 md:px-10 md:pt-9">
          <form className="flex flex-col gap-4" onSubmit={(e) => { e.preventDefault(); save() }}>
            <Field label="Name" hint={name ? 'Permanent. To rename, delete this worker and create a new one.' : 'Lower-case letters, digits and -. Permanent once created.'}>
              <Input value={form.name} placeholder="researcher" disabled={!!name} onChange={(e) => set('name', e.target.value)} />
            </Field>
            <div className="grid grid-cols-2 gap-3">
              <Field label="Engine">
                <select value={form.engine} onChange={(e) => changeEngine(e.target.value)} className="border-input h-8 rounded-lg border bg-transparent px-2 text-sm">
                  <option value="claude">Claude</option>
                  <option value="codex">Codex</option>
                </select>
              </Field>
              <Field label="Effort" hint="Blank uses the harness default.">
                <select value={form.effort} onChange={(e) => set('effort', e.target.value)} className="border-input h-8 rounded-lg border bg-transparent px-2 text-sm">
                  <option value="">Default</option>
                  {effortsFor(form.engine).map((e) => <option key={e} value={e}>{e}</option>)}
                </select>
              </Field>
            </div>
            <Field label="Model" hint="Blank uses the harness default.">
              <Input value={form.model} placeholder={form.engine === 'claude' ? claudeModels[0] : ''} onChange={(e) => set('model', e.target.value)} />
            </Field>
            {form.engine === 'claude' && (
              <Field label="Tools" hint="Comma-separated; blank allows the harness default (every tool).">
                <Input value={form.tools} placeholder="Read, Edit, Bash" onChange={(e) => set('tools', e.target.value)} />
              </Field>
            )}
            <Field label="Prompt" hint="The worker's whole job description. The Bob note and the project prompt are prepended automatically.">
              <Textarea value={form.prompt} rows={10} onChange={(e) => set('prompt', e.target.value)} />
            </Field>
            <Field label="Why" hint="Recorded with this version.">
              <Input value={why} placeholder="Why this change" onChange={(e) => setWhy(e.target.value)} />
            </Field>
            <div className="flex items-center gap-2">
              <Button type="submit" disabled={busy || !why.trim() || (!name && !form.name.trim())}>
                {busy ? 'Saving…' : name ? 'Save' : 'Create worker'}
              </Button>
              {name && !confirmDelete && (
                <Button type="button" variant="ghost" className="text-destructive" onClick={() => setConfirmDelete(true)}>Delete…</Button>
              )}
            </div>
            {name && confirmDelete && (
              <div className="border-destructive/40 flex flex-col gap-2 rounded-lg border p-3">
                <Field label="Why delete this worker?">
                  <Input value={deleteWhy} onChange={(e) => setDeleteWhy(e.target.value)} />
                </Field>
                <div className="flex items-center gap-2">
                  <Button type="button" variant="destructive" size="sm" disabled={busy || !deleteWhy.trim()} onClick={remove}>Delete {name}</Button>
                  <Button type="button" variant="ghost" size="sm" onClick={() => setConfirmDelete(false)}>Cancel</Button>
                </div>
                <p className="text-faint text-[12.5px]">Its chats stay readable but cannot send new messages; its schedules are deleted.</p>
              </div>
            )}
          </form>

          {name && (
            <Section title="History" count={versions?.length}>
              {!versions ? <Empty>Loading…</Empty>
                : versions.length === 0 ? <Empty>No versions yet.</Empty>
                : <VersionList versions={versions} />}
            </Section>
          )}
        </div>
      </div>
    </div>
  )
}

function VersionList({ versions }: { versions: WorkerVersion[] }) {
  const [open, setOpen] = useState<number | null>(null)
  return (
    <ul>
      {versions.map((v) => (
        <li key={v.id} className="border-t px-1 py-2.5 first:border-t-0">
          <button type="button" onClick={() => setOpen(open === v.id ? null : v.id)} className="flex w-full items-baseline gap-2 text-left text-[13.5px]">
            <span className="font-medium capitalize">{v.action}</span>
            <span className="text-faint">by {v.changed_by}, {when(v.changed_at)}</span>
            <span className="flex-1" />
            <span className="text-faint text-xs underline underline-offset-2">{open === v.id ? 'Hide prompt' : 'Show prompt'}</span>
          </button>
          <p className="text-muted-foreground pt-0.5 text-[13px]">{v.why}</p>
          {open === v.id && (
            <pre className="bg-muted mt-2 max-h-64 overflow-auto rounded-md p-2.5 text-xs whitespace-pre-wrap">{v.snapshot.prompt || '(empty)'}</pre>
          )}
        </li>
      ))}
    </ul>
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
