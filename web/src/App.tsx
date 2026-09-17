import { useCallback, useEffect, useState } from 'react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { api, Unauthorized, type Project, type Session, type WorkerList } from './api'
import { Chat } from './Chat'

// Routes are the URL hash: #/ · #/p/<project> · #/p/<project>/s/<session>
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
  const [config, setConfig] = useState<{ google_client_id: string; email: string } | null>(null)
  useEffect(() => { api.config().then(setConfig) }, [])
  if (!config) return null
  if (!config.email) return <SignIn clientId={config.google_client_id} onSignedIn={(email) => setConfig({ ...config, email })} />

  return <Signed email={config.email} onSignedOut={() => setConfig({ ...config, email: '' })} />
}

function Signed({ email, onSignedOut }: { email: string; onSignedOut: () => void }) {
  const hash = useHash()
  const [, , project, , session] = hash.split('/')
  const guard = useCallback((err: unknown) => {
    if (err instanceof Unauthorized) onSignedOut()
    else alert(String((err as Error)?.message ?? err))
  }, [onSignedOut])

  return (
    <div className="flex h-screen">
      <aside className="flex w-72 shrink-0 flex-col gap-4 overflow-y-auto border-r p-4 text-sm">
        <a href="#/" className="text-lg font-semibold">Bob</a>
        <Projects current={project} onError={guard} />
        {project && <ProjectPanel key={project} project={project} currentSession={session} onError={guard} />}
        <div className="mt-auto flex items-center justify-between text-xs text-muted-foreground">
          <span className="truncate">{email}</span>
          <Button variant="ghost" size="sm" onClick={() => api.logout().then(onSignedOut)}>Sign out</Button>
        </div>
      </aside>
      <main className="min-w-0 flex-1">
        {session ? <SessionView id={session} onError={guard} /> : <Empty project={project} />}
      </main>
    </div>
  )
}

function Empty({ project }: { project?: string }) {
  return (
    <div className="flex h-full items-center justify-center text-muted-foreground">
      {project ? 'Pick a worker to start a chat.' : 'Pick or create a project.'}
    </div>
  )
}

function Projects({ current, onError }: { current?: string; onError: (e: unknown) => void }) {
  const [projects, setProjects] = useState<Project[]>([])
  const [creating, setCreating] = useState(false)
  const load = useCallback(() => api.projects().then(setProjects).catch(onError), [onError])
  useEffect(() => { load() }, [load])

  return (
    <section className="flex flex-col gap-1">
      <h2 className="text-xs font-medium uppercase text-muted-foreground">Projects</h2>
      {projects.map((p) => (
        <a key={p.name} href={`#/p/${p.name}`} className={`rounded px-2 py-1 hover:bg-muted ${p.name === current ? 'bg-muted font-medium' : ''}`}>{p.name}</a>
      ))}
      {creating
        ? <CreateProject onDone={(name) => { setCreating(false); if (name) { load(); window.location.hash = `/p/${name}` } }} onError={onError} />
        : <Button variant="outline" size="sm" onClick={() => setCreating(true)}>New project</Button>}
    </section>
  )
}

function CreateProject({ onDone, onError }: { onDone: (name?: string) => void; onError: (e: unknown) => void }) {
  const [form, setForm] = useState({ name: '', repo_url: '', repo_ref: 'main', subfolder: '' })
  const field = (key: keyof typeof form, placeholder: string) => (
    <Input placeholder={placeholder} value={form[key]} onChange={(e) => setForm({ ...form, [key]: e.target.value })} />
  )
  return (
    <form className="flex flex-col gap-2 rounded border p-2" onSubmit={(e) => {
      e.preventDefault()
      api.createProject(form).then((p) => onDone(p.name)).catch(onError)
    }}>
      {field('name', 'name (a-z, 0-9, -)')}
      {field('repo_url', 'https://github.com/org/repo')}
      {field('repo_ref', 'branch')}
      {field('subfolder', 'subfolder (optional)')}
      <div className="flex gap-2">
        <Button type="submit" size="sm">Create</Button>
        <Button type="button" variant="ghost" size="sm" onClick={() => onDone()}>Cancel</Button>
      </div>
    </form>
  )
}

function ProjectPanel({ project, currentSession, onError }: { project: string; currentSession?: string; onError: (e: unknown) => void }) {
  const [list, setList] = useState<WorkerList | null>(null)
  const [sessions, setSessions] = useState<Session[]>([])
  const [busy, setBusy] = useState('')

  const loadSessions = useCallback(() => api.sessions(project).then(setSessions).catch(onError), [project, onError])
  useEffect(() => {
    setBusy('Starting project…')
    api.workers(project).then(setList).catch(onError).finally(() => setBusy(''))
    loadSessions()
  }, [project, onError, loadSessions])

  const sync = () => {
    setBusy('Pulling from git…')
    api.sync(project).then(setList).catch(onError).finally(() => setBusy(''))
  }
  const start = (worker: string) =>
    api.createSession(project, worker).then((s) => { loadSessions(); window.location.hash = `/p/${project}/s/${s.id}` }).catch(onError)

  return (
    <>
      <section className="flex flex-col gap-1">
        <div className="flex items-center justify-between">
          <h2 className="text-xs font-medium uppercase text-muted-foreground">Workers</h2>
          <Button variant="ghost" size="sm" onClick={sync} disabled={!!busy}>Sync git</Button>
        </div>
        {busy && <p className="text-xs text-muted-foreground">{busy}</p>}
        {list && !list.sync.ok && <p className="text-xs text-destructive">git: {list.sync.error}</p>}
        {list?.sync.commit && <p className="truncate text-xs text-muted-foreground" title={list.sync.commit}>at {list.sync.commit}</p>}
        {list?.error && <p className="text-xs text-destructive">{list.error}</p>}
        {list?.workers.map((w) => (
          <button key={w.name} onClick={() => start(w.name)} title={w.prompt}
            className="flex items-center justify-between rounded px-2 py-1 text-left hover:bg-muted">
            <span>{w.name}</span>
            <span className="text-xs text-muted-foreground">{w.engine}{w.model ? ` · ${w.model}` : ''}</span>
          </button>
        ))}
        {list && list.sync.ok && list.workers.length === 0 && <p className="text-xs text-muted-foreground">No workers/*.md in this folder yet.</p>}
      </section>
      <section className="flex flex-col gap-1">
        <h2 className="text-xs font-medium uppercase text-muted-foreground">Chats</h2>
        {sessions.map((s) => (
          <a key={s.id} href={`#/p/${project}/s/${s.id}`}
            className={`rounded px-2 py-1 hover:bg-muted ${s.id === currentSession ? 'bg-muted font-medium' : ''}`}>
            {s.worker} <span className="text-xs text-muted-foreground">{new Date(s.created_at).toLocaleString()}</span>
          </a>
        ))}
      </section>
    </>
  )
}

function SessionView({ id, onError }: { id: string; onError: (e: unknown) => void }) {
  const [session, setSession] = useState<Session | null>(null)
  useEffect(() => { api.session(id).then(setSession).catch(onError) }, [id, onError])
  return session ? <Chat key={session.id} session={session} /> : null
}

declare global {
  interface Window { google?: any }
}

function SignIn({ clientId, onSignedIn }: { clientId: string; onSignedIn: (email: string) => void }) {
  const [error, setError] = useState('')
  useEffect(() => {
    const script = document.createElement('script')
    script.src = 'https://accounts.google.com/gsi/client'
    script.onload = () => {
      window.google.accounts.id.initialize({
        client_id: clientId,
        callback: (r: { credential: string }) =>
          api.login(r.credential).then((x) => onSignedIn(x.email)).catch((e) => setError(e.message)),
      })
      window.google.accounts.id.renderButton(document.getElementById('google-button'), { theme: 'outline', size: 'large' })
    }
    document.body.appendChild(script)
    return () => { script.remove() }
  }, [clientId, onSignedIn])
  return (
    <div className="flex h-screen flex-col items-center justify-center gap-4">
      <h1 className="text-2xl font-semibold">Bob</h1>
      <div id="google-button" />
      {error && <p className="text-sm text-destructive">{error}</p>}
    </div>
  )
}
