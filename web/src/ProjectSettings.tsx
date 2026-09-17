import { useEffect, useState } from 'react'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { api, type Project } from './api'
import { SecretsEditor } from './SecretsEditor'

type Editable = Pick<Project, 'repo_url' | 'repo_ref' | 'subfolder' | 'image' | 'files_root'>

export function ProjectSettings({ name, open, onOpenChange, onSaved, onDeleted, onError }: {
  name: string
  open: boolean
  onOpenChange: (open: boolean) => void
  onSaved: () => void
  onDeleted: () => void
  onError: (e: unknown) => void
}) {
  const [form, setForm] = useState<Editable | null>(null)
  const [original, setOriginal] = useState<Editable | null>(null)
  const [busy, setBusy] = useState(false)
  const [confirmName, setConfirmName] = useState('')

  useEffect(() => {
    if (!open) return
    setConfirmName('')
    api.project(name).then((p) => {
      const editable = { repo_url: p.repo_url, repo_ref: p.repo_ref, subfolder: p.subfolder, image: p.image, files_root: p.files_root }
      setForm(editable)
      setOriginal(editable)
    }).catch(onError)
  }, [open, name, onError])

  const field = (key: keyof Editable, label: string, placeholder: string, hint?: string) => (
    <label className="flex flex-col gap-1 text-sm">
      <span className="font-medium">{label}</span>
      <Input value={form?.[key] ?? ''} placeholder={placeholder} onChange={(e) => form && setForm({ ...form, [key]: e.target.value })} />
      {hint && <span className="text-xs text-muted-foreground">{hint}</span>}
    </label>
  )
  const repoChanged = form && original && form.repo_url !== original.repo_url

  const save = () => {
    if (!form) return
    setBusy(true)
    api.updateProject(name, form).then(() => { onOpenChange(false); onSaved() }).catch(onError).finally(() => setBusy(false))
  }
  const remove = () => {
    setBusy(true)
    api.deleteProject(name).then(() => { onOpenChange(false); onDeleted() }).catch(onError).finally(() => setBusy(false))
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[92dvh] overflow-y-auto sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{name} settings</DialogTitle>
          <DialogDescription>Changing the repository, branch, subfolder or image restarts the project's computer and pulls from git. Chats are kept.</DialogDescription>
        </DialogHeader>

        {form && (
          <div className="flex flex-col gap-3">
            {field('repo_url', 'Repository', 'https://github.com/org/repo')}
            {repoChanged && (
              <p className="text-xs text-destructive">
                Pointing at a different repository breaks existing chats: their worktrees belong to the old one.
              </p>
            )}
            {field('repo_ref', 'Branch', 'main')}
            {field('subfolder', 'Subfolder', 'e.g. bob', 'Where workers/ and skills/ live. Empty = the repository root.')}
            {field('image', 'Image', 'bob-runtime:dev', 'Empty = the default runtime image.')}
            {field('files_root', 'Files folder', 'e.g. site', 'Where the Files page opens; it shows index.html there if there is one. Empty = the repository root.')}
          </div>
        )}

        <div className="flex justify-end gap-2">
          <Button variant="ghost" onClick={() => onOpenChange(false)} disabled={busy}>Cancel</Button>
          <Button onClick={save} disabled={busy || !form}>{busy ? 'Working…' : 'Save'}</Button>
        </div>

        <div className="border-t pt-3">
          <SecretsEditor project={name} onError={onError} />
        </div>

        <div className="mt-2 flex flex-col gap-2 rounded border border-destructive/40 p-3 text-sm">
          <span className="font-medium text-destructive">Delete project</span>
          <span className="text-xs text-muted-foreground">
            Removes the container, its volume (every chat's worktree and saved state) and all chats. Cannot be undone.
            Type <b>{name}</b> to confirm.
          </span>
          <div className="flex gap-2">
            <Input value={confirmName} onChange={(e) => setConfirmName(e.target.value)} placeholder={name} />
            <Button variant="destructive" onClick={remove} disabled={busy || confirmName !== name}>Delete</Button>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  )
}
