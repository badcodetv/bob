import { useCallback, useEffect, useState, type ReactNode } from 'react'
import { RefreshCwIcon } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'
import { api, type Project, type Session, type WorkerList } from './api'
import type { Activity } from './App'
import { ProjectSettings } from './ProjectSettings'
import { ago, engineName, PageBar, prettyModel, when, WorkerBadge } from './ui'

const RECENT = 5

/** A project's home: what it is, what's been happening in it, and who works in it. */
export function Overview({ name, admin, workers, sessions, activity, syncedAt, onSync, onError, onDeleted }: {
  name: string
  admin: boolean
  workers: WorkerList | null
  sessions: Session[]
  activity: Activity
  syncedAt?: Date
  onSync: () => void
  onError: (e: unknown) => void
  onDeleted: () => void
}) {
  const [project, setProject] = useState<Project | null>(null)
  const [settingsOpen, setSettingsOpen] = useState(false)
  const load = useCallback(() => api.project(name).then(setProject).catch(onError), [name, onError])
  useEffect(() => { load() }, [load])

  // Chats nobody wrote in (left over from before chats were created on first send) aren't news.
  const talked = sessions.filter((s) => s.messages)
  const recent = talked.slice(0, RECENT)
  const [commit, ...message] = (workers?.sync.commit ?? '').split(' ')
  const repo = project?.repo_url.replace(/^https:\/\//, '').replace(/\.git$/, '') ?? ''
  const repoLink = project?.repo_url.startsWith('https://') ? project.repo_url.replace(/\.git$/, '') : undefined
  const folderLink = repoLink && repoLink.includes('github.com')
    ? `${repoLink}/tree/${project!.repo_ref}${project!.subfolder ? `/${project!.subfolder}` : ''}` : repoLink
  const chatCount = (worker: string) => sessions.filter((s) => s.worker === worker).length

  return (
    <div className="flex h-full flex-col">
      <PageBar>
        <span className="text-[15px] font-semibold">Overview</span>
        <span className="flex-1" />
        <Button variant="outline" size="sm" onClick={onSync} disabled={!!activity}>
          <RefreshCwIcon className={cn(activity && 'animate-spin')} />
          {activity === 'syncing' ? 'Pulling…' : 'Sync from git'}
        </Button>
        {admin && <Button variant="outline" size="sm" onClick={() => setSettingsOpen(true)}>Settings</Button>}
      </PageBar>
      <ProjectSettings name={name} open={settingsOpen} onOpenChange={setSettingsOpen} onError={onError}
        onSaved={() => { load(); onSync() }} onDeleted={onDeleted} />

      <div className="min-h-0 flex-1 overflow-y-auto">
        <div className="mx-auto flex max-w-[1120px] flex-col gap-8 px-4 pt-6 pb-16 md:px-10 md:pt-9">
          <header className="flex flex-col gap-3.5">
            <h1 className="text-[32px] leading-none font-bold tracking-[-0.035em] break-words md:text-[40px]">{name}</h1>
            <dl className="flex flex-wrap gap-x-7 gap-y-2">
              <Fact label="Repository">
                <code className="font-mono text-[12.5px]">{repo.replace(/^github\.com\//, '')}</code>
              </Fact>
              <Fact label="Branch">
                <code className="font-mono text-[12.5px]">{project?.repo_ref}</code>
                {commit && <> at <code className="font-mono text-[12.5px]" title={message.join(' ')}>{commit}</code></>}
              </Fact>
              <Fact label="Last synced">
                {activity === 'starting' ? 'Starting…' : workers && !workers.sync.ok
                  ? <span className="text-destructive">Failed</span>
                  : ago(syncedAt) || '—'}
              </Fact>
              <Fact label="Chats">{talked.length}</Fact>
            </dl>
            {workers && !workers.sync.ok && (
              <p className="text-destructive max-w-[68ch] text-sm">Git sync failed: {workers.sync.error}</p>
            )}
          </header>

          <div className="grid items-start gap-8 md:grid-cols-[minmax(0,1.65fr)_minmax(0,1fr)] md:gap-12">
            <div className="flex min-w-0 flex-col gap-9">
              <Section title="Recent conversations" count={talked.length}>
                {recent.length ? (
                  <ul>
                    {recent.map((s) => (
                      <li key={s.id} className="border-t first:border-t-0">
                        <a href={`#/p/${name}/s/${s.id}`} className="hover:bg-accent grid grid-cols-[auto_minmax(0,1fr)_auto] items-start gap-3 rounded-lg px-2 py-3">
                          <span className="mt-px"><WorkerBadge name={s.worker} engine={s.engine} /></span>
                          <span className="min-w-0">
                            <span className={cn('block truncate text-[15px] font-medium', !s.title && 'text-muted-foreground italic')}>{s.title || 'Empty chat'}</span>
                            <span className="text-faint mt-0.5 block text-xs">
                              {s.worker}, {s.messages ?? 0} message{s.messages === 1 ? '' : 's'}
                              {s.model ? `, ${prettyModel(s.model)}` : ''}
                            </span>
                          </span>
                          <time className="text-faint pt-0.5 text-xs tabular-nums">{when(s.last_active_at ?? s.created_at)}</time>
                        </a>
                      </li>
                    ))}
                  </ul>
                ) : (
                  <Empty>No conversations yet. Start one with a worker from the list.</Empty>
                )}
                {talked.length > RECENT && (
                  <p className="text-faint px-2 text-[13px]">{talked.length - RECENT} older chats are listed under each worker in the sidebar.</p>
                )}
              </Section>

              <Section title="Memory">
                <Empty>No memories yet. Once workers can save what they learn, the newest memories show here with their labels.</Empty>
              </Section>
            </div>

            <div className="flex min-w-0 flex-col gap-9">
              <Section title="Workers" count={workers?.workers.length}>
                {!workers ? <Empty>Loading workers from git…</Empty>
                  : workers.workers.length === 0 ? <Empty>No workers yet. Add a <code className="font-mono text-xs">workers/*.md</code> file to the project's folder in git, then sync.</Empty>
                  : (
                    <ul>
                      {workers.workers.map((w) => (
                        <li key={w.name} className="flex flex-col gap-1.5 border-t px-1 py-3.5 first:border-t-0">
                          <div className="flex items-center gap-2.5">
                            <WorkerBadge name={w.name} engine={w.engine} />
                            <span className="text-[15px] font-semibold">{w.name}</span>
                            <Button variant="outline" size="sm" className="ml-auto" nativeButton={false} render={<a href={`#/p/${name}/new/${w.name}`} />}>New chat</Button>
                          </div>
                          <div className="text-faint text-[12.5px]">
                            {engineName(w.engine)}, {w.model ? prettyModel(w.model) : 'default model'}{w.effort ? `, ${w.effort} effort` : ''}
                          </div>
                          <p className="text-muted-foreground line-clamp-2 text-[13.5px]" title={w.prompt}>{w.prompt}</p>
                          <div className="text-faint text-[12.5px]">
                            {chatCount(w.name)} chat{chatCount(w.name) === 1 ? '' : 's'}, defined in <code className="font-mono text-xs">workers/{w.name}.md</code>
                          </div>
                        </li>
                      ))}
                    </ul>
                  )}
              </Section>

              <Section title="Setup" action={admin && <button type="button" onClick={() => setSettingsOpen(true)} className="text-muted-foreground hover:text-foreground text-[13px] underline underline-offset-2">Edit</button>}>
                <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-2.5 px-1 pt-1.5 text-[13.5px]">
                  <SetupRow label="Repository">
                    {folderLink ? <a href={folderLink} target="_blank" rel="noreferrer" className="decoration-border hover:decoration-foreground underline underline-offset-2">{repo}</a> : repo}
                  </SetupRow>
                  <SetupRow label="Folder"><code className="font-mono text-[12.5px]">{project?.subfolder || '(repository root)'}</code></SetupRow>
                  <SetupRow label="Branch"><code className="font-mono text-[12.5px]">{project?.repo_ref}</code></SetupRow>
                  {commit && <SetupRow label="Latest commit"><code className="font-mono text-[12.5px]">{commit}</code> {message.join(' ')}</SetupRow>}
                  <SetupRow label="Image"><code className="font-mono text-[12.5px]">{project?.image || 'bob-runtime'}</code>{!project?.image && <span className="text-faint"> (default)</span>}</SetupRow>
                  {project && <SetupRow label="Created">{new Date(project.created_at).toLocaleDateString([], { day: 'numeric', month: 'short', year: 'numeric' })}</SetupRow>}
                </dl>
              </Section>
            </div>
          </div>
        </div>
      </div>
    </div>
  )
}

function Fact({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="flex flex-col gap-px">
      <dt className="text-faint text-xs">{label}</dt>
      <dd className="text-[13.5px]">{children}</dd>
    </div>
  )
}

function Section({ title, count, action, children }: { title: string; count?: number; action?: ReactNode; children: ReactNode }) {
  return (
    <section className="flex flex-col gap-2.5">
      <div className="border-foreground flex items-baseline gap-2 border-b pb-2">
        <h2 className="text-base font-semibold tracking-tight">{title}</h2>
        {count !== undefined && <span className="text-faint text-[13px] tabular-nums">{count}</span>}
        {action && <span className="ml-auto">{action}</span>}
      </div>
      {children}
    </section>
  )
}

function SetupRow({ label, children }: { label: string; children: ReactNode }) {
  return (
    <>
      <dt className="text-faint pt-px text-[12.5px]">{label}</dt>
      <dd className="min-w-0 [overflow-wrap:anywhere]">{children}</dd>
    </>
  )
}

function Empty({ children }: { children: ReactNode }) {
  return <p className="text-muted-foreground px-2 py-3 text-sm">{children}</p>
}
