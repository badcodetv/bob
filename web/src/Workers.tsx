import { useCallback, useEffect, useState } from 'react'
import { PlusIcon } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { api, type Project, type Worker } from './api'
import { Empty, Section } from './Overview'
import { engineName, PageBar, prettyModel, when, WorkerBadge } from './ui'

/** A project's workers: its prompt (prepended to every worker's) and the list of who works here. */
export function Workers({ project, workers, onError }: {
  project: string
  workers: Worker[] | null
  onError: (e: unknown) => void
}) {
  const [prompt, setPrompt] = useState<Project | null>(null)
  const load = useCallback(() => api.project(project).then(setPrompt).catch(onError), [project, onError])
  useEffect(() => { load() }, [load])

  return (
    <div className="flex h-full flex-col">
      <PageBar>
        <span className="text-[15px] font-semibold">Workers</span>
        <span className="flex-1" />
        <Button size="sm" nativeButton={false} render={<a href={`#/p/${project}/workers/new`} />}><PlusIcon />New worker</Button>
      </PageBar>

      <div className="min-h-0 flex-1 overflow-y-auto">
        <div className="mx-auto flex max-w-[880px] flex-col gap-8 px-4 pt-6 pb-16 md:px-10 md:pt-9">
          <Section title="Project prompt"
            action={<a href={`#/p/${project}/prompt`} className="text-muted-foreground hover:text-foreground text-[13px] underline underline-offset-2">Edit</a>}>
            {prompt === null ? <Empty>Loading…</Empty>
              : prompt.prompt ? <p className="text-muted-foreground line-clamp-4 px-1 text-[13.5px]">{prompt.prompt}</p>
              : <Empty>No project prompt yet. It is prepended to every worker's prompt here, after the Bob note.</Empty>}
          </Section>

          <Section title="Workers" count={workers?.length}>
            {!workers ? <Empty>Loading…</Empty>
              : workers.length === 0 ? <Empty>No workers yet. Create one to start chatting.</Empty>
              : (
                <ul>
                  {workers.map((w) => (
                    <li key={w.id} className="border-t first:border-t-0">
                      <a href={`#/p/${project}/workers/${w.name}`} className="hover:bg-accent flex items-center gap-2.5 rounded-lg px-2 py-3">
                        <WorkerBadge name={w.name} engine={w.engine} />
                        <span className="min-w-0 flex-1">
                          <span className="block text-[15px] font-medium">{w.name}</span>
                          <span className="text-faint block text-xs">
                            {engineName(w.engine)}{w.model ? `, ${prettyModel(w.model)}` : ''}{w.effort ? `, ${w.effort} effort` : ''}
                          </span>
                        </span>
                        <span className="text-faint text-xs">Updated {when(w.updated_at)}</span>
                      </a>
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
