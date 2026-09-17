import { useCallback, useEffect, useState } from 'react'
import { ExternalLinkIcon, FileIcon, FolderIcon, RefreshCwIcon } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'
import { api, encodePath, type FileEntry, type Project, type WorkerList } from './api'
import type { Activity } from './App'
import { ago, PageBar } from './ui'

// Files that open in the viewer. Anything else gets a link instead: a sandboxed frame cannot
// download or run a plugin.
const viewable = /\.(html?|svg|png|jpe?g|gif|webp|avif|ico|txt|md|csv|jsonl?|ya?ml|log|css|js|xml)$/i

/**
 * The project's synced checkout, as the last git sync left it. Pages are shown in a sandboxed
 * frame through a viewer link, so an agent-written page runs no script and cannot act as you.
 * path is relative to the repository root; undefined means the project's files folder.
 */
export function Files({ project, path, workers, activity, syncedAt, onSync, onError }: {
  project: string
  path?: string
  workers: WorkerList | null
  activity: Activity
  syncedAt?: Date
  onSync: () => void
  onError: (e: unknown) => void
}) {
  const [info, setInfo] = useState<Project | null>(null)
  const [base, setBase] = useState('')
  useEffect(() => { api.project(project).then(setInfo).catch(onError) }, [project, onError])
  // Viewer links last 12 hours: get a fresh one on every git refresh and every 6 hours.
  useEffect(() => {
    const get = () => api.viewLink(project).then(setBase).catch(onError)
    get()
    const t = setInterval(get, 6 * 3600_000)
    return () => clearInterval(t)
  }, [project, onError, syncedAt])

  const root = info?.files_root ?? ''
  const current = path ?? root
  // The folder shown in the list: the path itself, or the folder holding the file.
  const [folder, setFolder] = useState<string | null>(null)
  const [entries, setEntries] = useState<FileEntry[] | null>(null)
  const [file, setFile] = useState<string | null>(null)
  const [missing, setMissing] = useState(false)

  const load = useCallback(async () => {
    if (!info) return
    setMissing(false)
    const parent = current.includes('/') ? current.slice(0, current.lastIndexOf('/')) : ''
    const name = current.slice(current.lastIndexOf('/') + 1)
    try {
      // Is current a folder or a file? Its parent's listing says.
      const siblings = current ? await api.listFiles(project, parent) : []
      const entry = siblings.find((e) => e.name === name)
      if (current && !entry) {
        setMissing(true)
        setEntries([])
        return
      }
      if (!current || entry?.type === 'dir') {
        const inside = await api.listFiles(project, current)
        // Opening the files folder shows its index.html, like a website.
        if (path === undefined && inside.some((e) => e.name === 'index.html')) {
          window.location.replace(`#/p/${project}/files/${encodePath(join(current, 'index.html'))}`)
          return
        }
        setFolder(current)
        setEntries(inside)
        setFile(null)
      } else {
        setFolder(parent)
        setEntries(siblings)
        setFile(current)
      }
    } catch (err) {
      onError(err)
    }
  }, [info, current, path, project, onError])
  useEffect(() => { load() }, [load, syncedAt])

  const href = (p: string) => `#/p/${project}/files/${encodePath(p)}`
  const crumbs = current.split('/').filter(Boolean)
  const [commit, ...subject] = (workers?.sync.commit ?? '').split(' ')
  const fileURL = file && base ? base + encodePath(file) : ''

  return (
    <div className="flex h-full flex-col">
      <PageBar>
        <nav className="flex min-w-0 flex-1 items-center gap-1 overflow-hidden text-[15px] whitespace-nowrap" aria-label="Path">
          <a href={href('')} className="font-semibold hover:underline">Files</a>
          {crumbs.map((c, i) => (
            <span key={i} className="flex min-w-0 items-center gap-1">
              <span className="text-faint">/</span>
              <a href={href(crumbs.slice(0, i + 1).join('/'))} className={cn('truncate hover:underline', i === crumbs.length - 1 && 'font-medium')}>{c}</a>
            </span>
          ))}
        </nav>
        <span className="text-faint hidden truncate text-[12.5px] lg:inline" title={subject.join(' ')}>
          {activity === 'syncing' ? 'Pulling from git…' : syncedAt ? `Updated from git ${ago(syncedAt)}${commit ? ` (${commit})` : ''}` : ''}
        </span>
        <Button variant="outline" size="sm" onClick={onSync} disabled={!!activity} title="Pull the latest from git and reload" aria-label="Refresh from git">
          <RefreshCwIcon className={cn(activity && 'animate-spin')} /><span className="hidden sm:inline">Refresh</span>
        </Button>
        {fileURL && (
          <Button variant="outline" size="sm" nativeButton={false} render={<a href={fileURL} target="_blank" rel="noreferrer" aria-label="Open in new tab" />}>
            <ExternalLinkIcon /><span className="hidden sm:inline">Open in new tab</span>
          </Button>
        )}
      </PageBar>

      <div className="flex min-h-0 flex-1">
        <ul className={cn('bg-sidebar/40 w-full shrink-0 overflow-y-auto border-r py-2 md:w-60', file && 'hidden md:block')}>
          {folder && (
            <li>
              <a href={href(folder.includes('/') ? folder.slice(0, folder.lastIndexOf('/')) : '')} className="text-muted-foreground hover:bg-accent flex items-center gap-2 px-4 py-1.5 text-[13.5px]">
                <FolderIcon className="size-3.5" />..
              </a>
            </li>
          )}
          {entries?.map((e) => {
            const p = join(folder ?? '', e.name)
            return (
              <li key={e.name}>
                <a href={href(p)} className={cn('hover:bg-accent flex items-center gap-2 px-4 py-1.5 text-[13.5px]', p === file && 'bg-muted font-medium')}>
                  {e.type === 'dir' ? <FolderIcon className="text-faint size-3.5 shrink-0" /> : <FileIcon className="text-faint size-3.5 shrink-0" />}
                  <span className="min-w-0 flex-1 truncate">{e.name}</span>
                  {e.type === 'file' && <span className="text-faint text-[11.5px] tabular-nums">{size(e.size)}</span>}
                </a>
              </li>
            )
          })}
          {entries?.length === 0 && !missing && <li className="text-muted-foreground px-4 py-2 text-sm">This folder is empty.</li>}
        </ul>

        <div className={cn('min-w-0 flex-1', !file && !missing && 'hidden md:block')}>
          {missing ? (
            <p className="text-muted-foreground p-6 text-sm">
              <code className="font-mono">{current}</code> is not in the checkout. It may not be pushed yet: press Refresh after it is.
            </p>
          ) : file && fileURL && viewable.test(file) ? (
            // sandbox="" : no scripts, no same-origin, no forms, no popups. The response's own
            // Content-Security-Policy says the same, for when the file is opened in a tab.
            <iframe key={`${fileURL}#${syncedAt?.getTime() ?? 0}`} src={fileURL} sandbox="" title={file} className="h-full w-full bg-white" />
          ) : file ? (
            <p className="text-muted-foreground p-6 text-sm">No preview for this kind of file. <a href={fileURL} target="_blank" rel="noreferrer" className="underline">Open it in a new tab</a>.</p>
          ) : (
            <p className="text-muted-foreground p-6 text-sm">
              Pick a file. This is the project's repository as of the last git sync{root ? <>; the Files page opens on <code className="font-mono">{root}</code></> : ''}.
            </p>
          )}
        </div>
      </div>
    </div>
  )
}

function join(folder: string, name: string) {
  return folder ? `${folder}/${name}` : name
}

function size(bytes: number) {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${Math.round(bytes / 1024)} KB`
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`
}
