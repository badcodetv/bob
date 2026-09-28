import { useCallback, useEffect, useState, type ReactNode } from 'react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { api, type PromptVersion } from './api'
import { Empty, Section } from './Overview'
import { PageBar, when } from './ui'

/** The project prompt: prepended (after the Bob note) to every worker's prompt, with its history. */
export function ProjectPrompt({ project, onError }: { project: string; onError: (e: unknown) => void }) {
  const [prompt, setPrompt] = useState('')
  const [why, setWhy] = useState('')
  const [versions, setVersions] = useState<PromptVersion[] | null>(null)
  const [busy, setBusy] = useState(false)

  const load = useCallback(() => api.projectPrompt(project).then((r) => { setPrompt(r.prompt); setVersions(r.versions) }).catch(onError), [project, onError])
  useEffect(() => { load() }, [load])

  const save = () => {
    if (!why.trim()) return
    setBusy(true)
    api.setProjectPrompt(project, prompt, why.trim()).then(() => { setWhy(''); load() }).catch(onError).finally(() => setBusy(false))
  }

  return (
    <div className="flex h-full flex-col">
      <PageBar>
        <span className="text-[15px] font-semibold">Project prompt</span>
        <span className="flex-1" />
        <Button variant="ghost" size="sm" nativeButton={false} render={<a href={`#/p/${project}/workers`} />}>Back to workers</Button>
      </PageBar>

      <div className="min-h-0 flex-1 overflow-y-auto">
        <div className="mx-auto flex max-w-[720px] flex-col gap-8 px-4 pt-6 pb-16 md:px-10 md:pt-9">
          <form className="flex flex-col gap-4" onSubmit={(e) => { e.preventDefault(); save() }}>
            <Field label="Prompt" hint="Prepended, after the Bob note, to every worker's prompt in this project.">
              <Textarea value={prompt} rows={12} onChange={(e) => setPrompt(e.target.value)} />
            </Field>
            <Field label="Why" hint="Recorded with this version.">
              <Input value={why} placeholder="Why this change" onChange={(e) => setWhy(e.target.value)} />
            </Field>
            <Button type="submit" className="self-start" disabled={busy || !why.trim()}>{busy ? 'Saving…' : 'Save'}</Button>
          </form>

          <Section title="History" count={versions?.length}>
            {!versions ? <Empty>Loading…</Empty>
              : versions.length === 0 ? <Empty>No changes yet.</Empty>
              : (
                <ul>
                  {versions.map((v) => (
                    <li key={v.id} className="border-t px-1 py-2.5 first:border-t-0">
                      <div className="text-[13.5px] font-medium">{when(v.changed_at)} <span className="text-faint font-normal">by {v.changed_by}</span></div>
                      <p className="text-muted-foreground pt-0.5 text-[13px]">{v.why}</p>
                      <pre className="bg-muted mt-1.5 max-h-48 overflow-auto rounded-md p-2.5 text-xs whitespace-pre-wrap">{v.prompt || '(empty)'}</pre>
                    </li>
                  ))}
                </ul>
              )}
          </Section>
        </div>
      </div>
    </div>
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
