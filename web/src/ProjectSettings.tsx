import { useEffect, useState } from 'react'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { api, type Project } from './api'

/**
 * Where a project's settings come from. They are not editable here: a project is declared in the
 * deploy's compose file and listed in Bob's projects file, so changing one is a deploy. This
 * dialog exists so you can see what this Bob is actually running.
 */
export function ProjectSettings({ name, open, onOpenChange, onError }: {
  name: string
  open: boolean
  onOpenChange: (open: boolean) => void
  onError: (e: unknown) => void
}) {
  const [project, setProject] = useState<Project | null>(null)

  useEffect(() => {
    if (!open) return
    api.project(name).then(setProject).catch(onError)
  }, [open, name, onError])

  const row = (label: string, value: string, empty: string) => (
    <div className="flex flex-col gap-0.5 text-sm">
      <span className="font-medium">{label}</span>
      <span className={value ? 'font-mono text-xs break-all' : 'text-muted-foreground text-xs'}>{value || empty}</span>
    </div>
  )

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[92dvh] overflow-y-auto sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{name} settings</DialogTitle>
          <DialogDescription>
            Read-only. This project is declared in Bob's projects file and has its own container in
            the deploy's compose file, so changing any of this — or adding and removing projects —
            is a deploy.
          </DialogDescription>
        </DialogHeader>

        {project && (
          <div className="flex flex-col gap-3">
            {row('Repository', project.repo_url, 'none')}
            {row('Branch', project.repo_ref, 'main')}
            {row('Subfolder', project.subfolder, 'the repository root')}
            {row('Image', project.image, 'the default runtime image')}
          </div>
        )}

        <div className="flex justify-end">
          <Button variant="ghost" onClick={() => onOpenChange(false)}>Close</Button>
        </div>
      </DialogContent>
    </Dialog>
  )
}
