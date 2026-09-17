// Thin client for Bob's API. Every call is same-origin; the session cookie does the auth.

export interface Project { name: string; repo_url: string; repo_ref: string; subfolder: string; image: string; files_root: string; created_at: string }
export interface FileEntry { name: string; type: 'file' | 'dir' | 'link' | 'other'; size: number }
export interface Worker { name: string; engine: string; model?: string; effort?: string; tools?: string[]; prompt: string }
export interface WorkerList { sync: { ok: boolean; commit?: string; error?: string }; workers: Worker[]; error?: string }
export interface Session {
  id: string; project: string; worker: string; engine: string; harness_session_id: string; model: string; effort: string; created_at: string
  // Only on the project's session list: the first message, messages sent, and the last activity.
  title?: string; messages?: number; last_active_at?: string
  /** The schedule that started this chat, if one did. */
  schedule?: string
}
export interface Schedule {
  id: string; project: string; name: string; worker: string; cron: string; timezone: string; message: string
  enabled: boolean; keep_sessions: number; created_at: string
}
export interface ScheduleRun {
  id: number; schedule_id: string; session_id: string | null; trigger: 'cron' | 'manual'
  status: 'running' | 'ok' | 'failed' | 'skipped'; detail: string; started_at: string; finished_at: string | null
}
export interface ScheduleView extends Schedule { next_at: string | null; last_run: ScheduleRun | null }
export type ScheduleInput = Pick<Schedule, 'name' | 'worker' | 'cron' | 'timezone' | 'message' | 'enabled' | 'keep_sessions'>
export interface Settings { model: string; effort: string }
export interface BobEvent { id: number; session_id: string; engine: string; kind: string; payload: any; created_at: string }

/** Who is signed in; admin ("*" in the project map) may create, change and delete projects. */
export type Config = { google_client_id: string; email: string; admin: boolean }

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
  config: () => call<Config>('GET', '/api/config'),
  login: (credential: string) => call<{ email: string }>('POST', '/api/login', { credential }),
  logout: () => call('POST', '/api/logout'),
  projects: () => call<{ projects: Project[] }>('GET', '/api/projects').then((r) => r.projects),
  createProject: (p: Partial<Project>) => call<Project>('POST', '/api/projects', p),
  project: (name: string) => call<Project>('GET', `/api/projects/${name}`),
  updateProject: (name: string, p: Pick<Project, 'repo_url' | 'repo_ref' | 'subfolder' | 'image' | 'files_root'>) => call<Project>('PATCH', `/api/projects/${name}`, p),
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
  schedules: (project: string) => call<{ schedules: ScheduleView[]; paused: boolean }>('GET', `/api/projects/${project}/schedules`),
  createSchedule: (project: string, s: ScheduleInput) => call<Schedule>('POST', `/api/projects/${project}/schedules`, s),
  updateSchedule: (id: string, s: Partial<ScheduleInput>) => call<Schedule>('PATCH', `/api/schedules/${id}`, s),
  deleteSchedule: (id: string) => call('DELETE', `/api/schedules/${id}`),
  runSchedule: (id: string) => call<ScheduleRun>('POST', `/api/schedules/${id}/run`),
  scheduleRuns: (id: string) => call<{ runs: ScheduleRun[] }>('GET', `/api/schedules/${id}/runs`).then((r) => r.runs),
  /** A folder of the project's synced checkout; path is relative to the repository root. */
  listFiles: (project: string, path: string) => call<{ entries: FileEntry[] }>('GET', `/api/projects/${project}/files/${encodePath(path)}`).then((r) => r.entries),
  /** A link prefix under which the project's files load without the cookie, for sandboxed pages. */
  viewLink: (project: string) => call<{ base: string }>('POST', `/api/projects/${project}/view`).then((r) => r.base),
  settings: () => call<{ schedules_paused: boolean }>('GET', '/api/settings'),
  updateSettings: (s: { schedules_paused: boolean }) => call<{ schedules_paused: boolean }>('PATCH', '/api/settings', s),
}

/** Encodes each segment of a slash-separated path. */
export const encodePath = (path: string) => path.split('/').filter(Boolean).map(encodeURIComponent).join('/')
