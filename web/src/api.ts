// Thin client for Bob's API. Every call is same-origin; the session cookie does the auth.

export interface Project { name: string; prompt: string; created_at: string }
export interface FileEntry { name: string; type: 'file' | 'dir' | 'link' | 'other'; size: number }
export interface Worker {
  id: string; project: string; name: string; engine: string; model: string; effort: string
  tools?: string[]; prompt: string; labels: Record<string, string>
  created_by: string; created_at: string; updated_by: string; updated_at: string
}
/** One create, update or delete of a worker: a full snapshot of it, who changed it, when and why. */
export interface WorkerVersion {
  id: number; worker_id: string; project: string; name: string; action: 'create' | 'update' | 'delete'
  snapshot: { prompt?: string; [key: string]: unknown }; why: string; changed_by: string; changed_at: string
}
export interface PromptVersion { id: number; prompt: string; why: string; changed_by: string; changed_at: string }
/** What a worker needs to be created or changed; name and why are only required by the server. */
export type WorkerInput = { name?: string; engine: string; model?: string; effort?: string; tools?: string[]; prompt: string; labels?: Record<string, string>; why: string }
export interface Session {
  id: string; project: string; worker: string; engine: string; harness_session_id: string; model: string; effort: string; created_at: string
  // Only on the project's session list: the first message, messages sent, and the last activity.
  title?: string; messages?: number; last_active_at?: string
  /** The schedule that started this chat, if one did. */
  schedule?: string
  /** Set when the session's worker has been deleted (list only): readable, but takes no more messages. */
  worker_removed?: boolean
}
export interface Schedule {
  id: string; project: string; name: string; worker: string; cron: string; timezone: string; message: string
  enabled: boolean; keep_sessions: number; created_at: string
}
export interface ScheduleRun {
  id: number; schedule_id: string; session_id: string | null; trigger: 'cron' | 'manual'
  /** queued: waiting for the project's current scheduled run to finish (one at a time per project). */
  status: 'queued' | 'running' | 'ok' | 'failed' | 'skipped'; detail: string; started_at: string; finished_at: string | null
}
export interface ScheduleView extends Schedule { next_at: string | null; last_run: ScheduleRun | null }
export type ScheduleInput = Pick<Schedule, 'name' | 'worker' | 'cron' | 'timezone' | 'message' | 'enabled' | 'keep_sessions'>
export interface Settings { model: string; effort: string }
/** One memory, as memory_search and GET .../memories return it: a snippet, not the whole content. */
export interface Memory {
  id: string; labels: Record<string, string>; snippet: string; score: number
  created_by_worker: string; created_by_session: string; chat_url: string; created_at: string
}
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
  project: (name: string) => call<Project>('GET', `/api/projects/${name}`),
  workers: (project: string) => call<{ workers: Worker[] }>('GET', `/api/projects/${project}/workers`).then((r) => r.workers),
  createWorker: (project: string, w: WorkerInput) => call<Worker>('POST', `/api/projects/${project}/workers`, w),
  updateWorker: (project: string, name: string, w: Omit<WorkerInput, 'name'>) => call<Worker>('PATCH', `/api/projects/${project}/workers/${encodeURIComponent(name)}`, w),
  deleteWorker: (project: string, name: string, why: string) => call<{ ok: boolean }>('DELETE', `/api/projects/${project}/workers/${encodeURIComponent(name)}?why=${encodeURIComponent(why)}`),
  workerVersions: (project: string, name: string) => call<{ versions: WorkerVersion[] }>('GET', `/api/projects/${project}/workers/${encodeURIComponent(name)}/versions`).then((r) => r.versions),
  projectPrompt: (project: string) => call<{ prompt: string; versions: PromptVersion[] }>('GET', `/api/projects/${project}/prompt`),
  setProjectPrompt: (project: string, prompt: string, why: string) => call<{ prompt: string }>('PUT', `/api/projects/${project}/prompt`, { prompt, why }),
  sessions: (project: string) => call<{ sessions: Session[] }>('GET', `/api/projects/${project}/sessions`).then((r) => r.sessions),
  createSession: (project: string, worker: string, engine: string, settings: Settings) => call<Session>('POST', `/api/projects/${project}/sessions`, { worker, engine, ...settings }),
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
  memories: (project: string, limit = 20) => call<{ memories: Memory[] }>('GET', `/api/projects/${project}/memories?limit=${limit}`).then((r) => r.memories),
  /** A folder of the project's synced checkout; path is relative to the repository root. */
  listFiles: (project: string, path: string) => call<{ entries: FileEntry[] }>('GET', `/api/projects/${project}/files/${encodePath(path)}`).then((r) => r.entries),
  /** A link prefix under which the project's files load without the cookie, for sandboxed pages. */
  viewLink: (project: string) => call<{ base: string }>('POST', `/api/projects/${project}/view`).then((r) => r.base),
  /** applied: false means a turn was running, so the project's container keeps the old values until it restarts. */
  settings: () => call<{ schedules_paused: boolean }>('GET', '/api/settings'),
  updateSettings: (s: { schedules_paused: boolean }) => call<{ schedules_paused: boolean }>('PATCH', '/api/settings', s),
}

/** Encodes each segment of a slash-separated path. */
export const encodePath = (path: string) => path.split('/').filter(Boolean).map(encodeURIComponent).join('/')
