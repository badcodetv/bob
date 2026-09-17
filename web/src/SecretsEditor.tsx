import { useCallback, useEffect, useState } from 'react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { api, type SecretInfo } from './api'
import { when } from './ui'

/**
 * A project's secrets: environment variables for its container, stored encrypted. Values can be
 * set or replaced here but never read back.
 */
export function SecretsEditor({ project, onError }: { project: string; onError: (e: unknown) => void }) {
  const [list, setList] = useState<SecretInfo[] | null>(null)
  const [enabled, setEnabled] = useState(true)
  const [name, setName] = useState('')
  const [value, setValue] = useState('')
  const [busy, setBusy] = useState(false)
  const [note, setNote] = useState('')

  const load = useCallback(() => api.secrets(project).then((r) => { setList(r.secrets); setEnabled(r.enabled) }).catch(onError), [project, onError])
  useEffect(() => { load() }, [load])

  const applied = (r: { applied: boolean }, what: string) => {
    setNote(r.applied
      ? `${what} The project's computer restarted to pick it up.`
      : `${what} A chat is running, so it applies when the project next restarts.`)
    load()
  }
  const save = (e: React.FormEvent) => {
    e.preventDefault()
    setBusy(true)
    const n = name.trim()
    api.setSecret(project, n, value)
      .then((r) => { setName(''); setValue(''); applied(r, `Saved ${n}.`) })
      .catch(onError).finally(() => setBusy(false))
  }
  const remove = (n: string) => {
    if (!confirm(`Delete the secret ${n}?`)) return
    setBusy(true)
    api.deleteSecret(project, n).then((r) => applied(r, `Deleted ${n}.`)).catch(onError).finally(() => setBusy(false))
  }

  return (
    <div className="flex flex-col gap-2 text-sm">
      <span className="font-medium">Secrets</span>
      <span className="text-muted-foreground text-xs">
        Environment variables for this project's computer only, such as an API key. Stored encrypted; nobody can read a value
        back here, but a worker can use it, and could repeat it in a chat. A secret replaces a shared variable of the same name.
      </span>
      {!enabled && <p className="text-destructive text-xs">Secrets are off: Bob needs BOB_SECRETS_KEY set.</p>}
      {list && list.length > 0 && (
        <ul className="flex flex-col">
          {list.map((s) => (
            <li key={s.name} className="flex items-center gap-2 border-t py-1.5 first:border-t-0">
              <code className="font-mono text-[12.5px]">{s.name}</code>
              <span className="text-faint min-w-0 flex-1 truncate text-xs">set {when(s.updated_at)}{s.updated_by ? ` by ${s.updated_by}` : ''}</span>
              <Button variant="ghost" size="xs" onClick={() => { setName(s.name); setValue('') }} disabled={busy}>Replace</Button>
              <Button variant="ghost" size="xs" className="text-destructive" onClick={() => remove(s.name)} disabled={busy}>Delete</Button>
            </li>
          ))}
        </ul>
      )}
      <form className="flex flex-col gap-2 sm:flex-row" onSubmit={save}>
        <Input value={name} placeholder="NAME" className="font-mono sm:w-44" autoComplete="off" spellCheck={false}
          onChange={(e) => setName(e.target.value.toUpperCase().replace(/[^A-Z0-9_]/g, '_'))} />
        <Input value={value} type="password" placeholder="value" autoComplete="new-password" onChange={(e) => setValue(e.target.value)} />
        <Button type="submit" variant="outline" disabled={busy || !enabled || !name.trim() || !value}>Save</Button>
      </form>
      {note && <p className="text-muted-foreground text-xs">{note}</p>}
    </div>
  )
}
