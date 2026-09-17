import { useCallback, useEffect, useState } from 'react'
import { api, Unauthorized, type Config, type Project, type Session, type Worker, type WorkerList } from './api'
import { Chat, NewChat } from './Chat'
import { Overview } from './Overview'
import { Schedules } from './Schedules'
import { Files } from './Files'
import { Sidebar } from './Sidebar'
import { DrawerContext } from './ui'
import { cn } from '@/lib/utils'

// Routes are the URL hash: #/ · #/p/<project> (its overview) · #/p/<project>/s/<session> · #/p/<project>/new/<worker>
// · #/p/<project>/schedules · #/p/<project>/files (its files folder) · #/p/<project>/files/<path> ('' = the repository root)
function useHash() {
  const [hash, setHash] = useState(window.location.hash.slice(1) || '/')
  useEffect(() => {
    const on = () => setHash(window.location.hash.slice(1) || '/')
    window.addEventListener('hashchange', on)
    return () => window.removeEventListener('hashchange', on)
  }, [])
  return hash
}

export default function App() {
  const [config, setConfig] = useState<Config | null>(null)
  const load = useCallback(() => { api.config().then(setConfig) }, [])
  useEffect(load, [load])
  if (!config) return null
  if (!config.email) return <SignIn clientId={config.google_client_id} onSignedIn={load} />

  return <Signed email={config.email} admin={config.admin} onSignedOut={() => setConfig({ ...config, email: '', admin: false })} />
}

/** Which of a project's pages is open. */
export type Page = 'overview' | 'schedules' | 'files' | 'chat'

/** What the sidebar says the project is doing right now. */
export type Activity = '' | 'starting' | 'syncing'

function Signed({ email, admin, onSignedOut }: { email: string; admin: boolean; onSignedOut: () => void }) {
  const hash = useHash()
  const [, , project, mode, id, ...rest] = hash.split('/')
  const session = mode === 's' ? id : undefined
  const newWorker = mode === 'new' ? id : undefined
  const page: Page = session || newWorker ? 'chat' : mode === 'schedules' ? 'schedules' : mode === 'files' ? 'files' : 'overview'
  const filePath = mode === 'files' && id !== undefined ? [id, ...rest].map(decodeURIComponent).filter(Boolean).join('/') : undefined
  const [drawer, setDrawer] = useState(false)
  useEffect(() => { setDrawer(false) }, [hash])

  const guard = useCallback((err: unknown) => {
    if (err instanceof Unauthorized) onSignedOut()
    else alert(String((err as Error)?.message ?? err))
  }, [onSignedOut])

  const [projects, setProjects] = useState<Project[]>([])
  const loadProjects = useCallback(() => api.projects().then(setProjects).catch(guard), [guard])
  useEffect(() => { loadProjects() }, [loadProjects])

  // The open project's workers and chats, shared by the sidebar and the overview.
  const [workers, setWorkers] = useState<WorkerList | null>(null)
  const [syncedAt, setSyncedAt] = useState<Date>()
  const [activity, setActivity] = useState<Activity>('')
  const [sessions, setSessions] = useState<Session[]>([])

  const loadSessions = useCallback(() => {
    if (project) api.sessions(project).then(setSessions).catch(guard)
  }, [project, guard])

  useEffect(() => {
    setWorkers(null)
    setSessions([])
    if (!project) return
    setActivity('starting')
    api.workers(project).then((l) => { setWorkers(l); setSyncedAt(new Date()) }).catch(guard).finally(() => setActivity(''))
    loadSessions()
  }, [project, guard, loadSessions])

  const sync = useCallback(() => {
    if (!project) return
    setActivity('syncing')
    api.sync(project).then((l) => { setWorkers(l); setSyncedAt(new Date()) }).catch(guard).finally(() => setActivity(''))
  }, [project, guard])

  const findWorker = (name?: string) => workers?.workers.find((w) => w.name === name)

  return (
    <DrawerContext.Provider value={() => setDrawer(true)}>
      <div className="flex h-dvh">
        <div className={cn(
          'fixed inset-y-0 left-0 z-40 w-[min(300px,86vw)] -translate-x-full transition-transform duration-200 md:static md:z-auto md:w-68 md:translate-x-0 md:transition-none',
          drawer && 'translate-x-0',
        )}>
          <Sidebar email={email} admin={admin} projects={projects} project={project} workers={workers} sessions={sessions}
            activity={activity} syncedAt={syncedAt} currentSession={session} currentWorker={newWorker}
            page={page} onSync={sync} onError={guard}
            onProjectCreated={(name) => { loadProjects(); window.location.hash = `/p/${name}` }}
            onSignOut={() => api.logout().then(onSignedOut)} />
        </div>
        {drawer && <div className="fixed inset-0 z-30 bg-black/30 md:hidden" onClick={() => setDrawer(false)} />}

        <main className="min-w-0 flex-1">
          {session ? <SessionView id={session} findWorker={findWorker} onError={guard} onChanged={loadSessions} onDeleted={() => {
                loadSessions()
                window.location.hash = `/p/${project}`
              }} />
            : project && newWorker ? <NewChat key={newWorker} project={project} worker={findWorker(newWorker)} loading={!workers} onCreated={(s) => {
                loadSessions()
                window.location.hash = `/p/${project}/s/${s.id}`
              }} />
            : project && page === 'files' ? <Files key={project} project={project} path={filePath} workers={workers} activity={activity} syncedAt={syncedAt} onSync={sync} onError={guard} />
            : project && page === 'schedules' ? <Schedules key={project} project={project} admin={admin} workers={workers} onRan={loadSessions} onError={guard} />
            : project ? <Overview key={project} name={project} admin={admin} workers={workers} sessions={sessions} activity={activity}
                syncedAt={syncedAt} onSync={sync} onError={guard}
                onDeleted={() => { loadProjects(); window.location.hash = '/' }} />
            : <Home projects={projects} />}
        </main>
      </div>
    </DrawerContext.Provider>
  )
}

function Home({ projects }: { projects: Project[] }) {
  return (
    <div className="flex h-full flex-col justify-center gap-3 px-6">
      <div className="mx-auto flex w-full max-w-md flex-col gap-3">
        <h1 className="text-3xl font-semibold tracking-tight">Pick a project</h1>
        <p className="text-muted-foreground">
          {projects.length
            ? 'Choose one from the menu at the top of the sidebar, or open one here.'
            : 'There are no projects you can open yet.'}
        </p>
        <ul className="flex flex-col">
          {projects.map((p) => (
            <li key={p.name} className="border-t first:border-t-0">
              <a href={`#/p/${p.name}`} className="hover:bg-accent -mx-2 flex items-baseline justify-between rounded-lg px-2 py-2.5">
                <span className="text-[15px] font-medium">{p.name}</span>
                <span className="text-faint truncate pl-4 font-mono text-xs">{p.repo_url.replace(/^https:\/\//, '')}</span>
              </a>
            </li>
          ))}
        </ul>
      </div>
    </div>
  )
}

function SessionView({ id, findWorker, onError, onChanged, onDeleted }: {
  id: string; findWorker: (name?: string) => Worker | undefined; onError: (e: unknown) => void; onChanged: () => void; onDeleted: () => void
}) {
  const [session, setSession] = useState<Session | null>(null)
  useEffect(() => { api.session(id).then(setSession).catch(onError) }, [id, onError])
  return session ? <Chat key={session.id} session={session} worker={findWorker(session.worker)} onSent={onChanged} onDeleted={onDeleted} /> : null
}

declare global {
  interface Window { google?: any }
}

function SignIn({ clientId, onSignedIn }: { clientId: string; onSignedIn: () => void }) {
  const [error, setError] = useState('')
  useEffect(() => {
    const script = document.createElement('script')
    script.src = 'https://accounts.google.com/gsi/client'
    script.onload = () => {
      window.google.accounts.id.initialize({
        client_id: clientId,
        callback: (r: { credential: string }) =>
          api.login(r.credential).then(() => onSignedIn()).catch((e) => setError(e.message)),
      })
      window.google.accounts.id.renderButton(document.getElementById('google-button'), { theme: 'outline', size: 'large' })
    }
    document.body.appendChild(script)
    return () => { script.remove() }
  }, [clientId, onSignedIn])
  return (
    <div className="flex h-dvh flex-col items-center justify-center gap-5 px-6">
      <h1 className="text-4xl font-bold tracking-tight">Bob</h1>
      <p className="text-muted-foreground">Sign in with your BadCode Google account.</p>
      <div id="google-button" />
      {error && <p className="text-destructive text-sm">{error}</p>}
    </div>
  )
}
