// Thin client for Bob's API. Every call is same-origin; the session cookie does the auth.

export interface Project { name: string; repo_url: string; repo_ref: string; subfolder: string; image: string; created_at: string }
export interface Worker { name: string; engine: string; model?: string; effort?: string; tools?: string[]; prompt: string }
export interface WorkerList { sync: { ok: boolean; commit?: string; error?: string }; workers: Worker[]; error?: string }
export interface Session { id: string; project: string; worker: string; engine: string; harness_session_id: string; model: string; effort: string; created_at: string }
export interface Settings { model: string; effort: string }
export interface BobEvent { id: number; session_id: string; engine: string; kind: string; payload: any; created_at: string }

export class Unauthorized extends Error {}

async function call<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(path, {
    method,
    headers: body === undefined ? undefined : { 'content-type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  if (res.status === 401) throw new Unauthorized('sign in first')
  if (!res.ok) throw new Error((await res.text()).trim() || res.statusText)
  return res.json() as Promise<T>
}

export const api = {
  config: () => call<{ google_client_id: string; email: string }>('GET', '/api/config'),
  login: (credential: string) => call<{ email: string }>('POST', '/api/login', { credential }),
  logout: () => call('POST', '/api/logout'),
  projects: () => call<{ projects: Project[] }>('GET', '/api/projects').then((r) => r.projects),
  createProject: (p: Partial<Project>) => call<Project>('POST', '/api/projects', p),
  project: (name: string) => call<Project>('GET', `/api/projects/${name}`),
  updateProject: (name: string, p: Pick<Project, 'repo_url' | 'repo_ref' | 'subfolder' | 'image'>) => call<Project>('PATCH', `/api/projects/${name}`, p),
  deleteProject: (name: string) => call('DELETE', `/api/projects/${name}`),
  restartProject: (name: string) => call('POST', `/api/projects/${name}/restart`),
  workers: (project: string) => call<WorkerList>('GET', `/api/projects/${project}/workers`),
  sync: (project: string) => call<WorkerList>('POST', `/api/projects/${project}/sync`),
  sessions: (project: string) => call<{ sessions: Session[] }>('GET', `/api/projects/${project}/sessions`).then((r) => r.sessions),
  createSession: (project: string, worker: string, settings: Settings) => call<Session>('POST', `/api/projects/${project}/sessions`, { worker, ...settings }),
  updateSession: (id: string, settings: Settings) => call<Session>('PATCH', `/api/sessions/${id}`, settings),
  deleteSession: (id: string) => call('DELETE', `/api/sessions/${id}`),
  session: (id: string) => call<Session>('GET', `/api/sessions/${id}`),
  send: (id: string, text: string) => call<BobEvent>('POST', `/api/sessions/${id}/messages`, { text }),
  interrupt: (id: string) => call('POST', `/api/sessions/${id}/interrupt`),
}
