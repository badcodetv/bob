import { useState } from 'react'
import { MoreHorizontalIcon } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { cn } from '@/lib/utils'
import type { Settings, Worker } from './api'
import { claudeEfforts, claudeModels } from './engines/claude'
import { engineName, Menu, MenuItem, MenuNote, MenuSeparator, PageBar, prettyModel, WorkerBadge } from './ui'

// Choices offered per engine. An empty value means "the worker's own setting".
const options: Record<string, { model: string[]; effort: string[] }> = {
  claude: { model: claudeModels, effort: claudeEfforts },
}

export function ChatHeader({ worker, engine, title, workerDefaults, settings, onChange, onDelete }: {
  worker: string
  engine: string
  title?: string
  workerDefaults?: Pick<Worker, 'model' | 'effort'>
  settings: Settings
  onChange: (s: Settings) => void
  onDelete?: () => void
}) {
  const [confirming, setConfirming] = useState(false)
  const [deleting, setDeleting] = useState(false)
  const opts = options[engine] ?? { model: [], effort: [] }

  const picker = (key: keyof Settings, label: string) => {
    const own = settings[key]
    const fallback = workerDefaults?.[key]
    const show = (v: string) => (key === 'model' ? prettyModel(v) : v)
    const values = own && !opts[key].includes(own) ? [own, ...opts[key]] : opts[key]
    return (
      <Menu label={`${label} for this chat`} align="end"
        triggerClassName="hover:bg-accent data-popup-open:bg-accent inline-flex items-baseline gap-1.5 rounded-md px-2 py-1 text-[13px]"
        trigger={<>
          <span className="text-faint hidden text-xs sm:inline">{label}</span>
          <span className={cn(own && 'text-cobalt')}>{own ? show(own) : fallback ? show(fallback) : 'Default'}</span>
        </>}>
        <MenuNote>{label} for this chat</MenuNote>
        <MenuItem checked={!own} hint={fallback ? show(fallback) : 'default'} onClick={() => onChange({ ...settings, [key]: '' })}>
          Worker's choice
        </MenuItem>
        {values.length > 0 && <MenuSeparator />}
        {values.map((v) => (
          <MenuItem key={v} checked={own === v} onClick={() => onChange({ ...settings, [key]: v })}>{show(v)}</MenuItem>
        ))}
      </Menu>
    )
  }

  return (
    <PageBar>
      <div className="flex min-w-0 items-center gap-2.5">
        <WorkerBadge name={worker} engine={engine} />
        <span className="shrink-0 text-[15px] font-semibold">{worker}</span>
        <span className="text-faint hidden text-xs sm:inline">{engineName(engine)}</span>
        {title && <span className="text-muted-foreground hidden min-w-0 truncate pl-1 text-[13px] lg:inline">{title}</span>}
      </div>
      <span className="flex-1" />
      {picker('model', 'Model')}
      {picker('effort', 'Effort')}
      {onDelete && (
        <>
          <Menu label="More actions" align="end"
            triggerClassName="text-muted-foreground hover:bg-accent hover:text-foreground data-popup-open:bg-accent grid size-8 place-items-center rounded-lg"
            trigger={<MoreHorizontalIcon className="size-[18px]" />}>
            <MenuItem danger onClick={() => setConfirming(true)}>Delete chat…</MenuItem>
          </Menu>
          <Dialog open={confirming} onOpenChange={setConfirming}>
            <DialogContent className="sm:max-w-md">
              <DialogHeader>
                <DialogTitle>Delete this chat?</DialogTitle>
                <DialogDescription>
                  {title ? <>“{title}” and </> : 'Its messages and '}its git branch are removed. This can't be undone.
                </DialogDescription>
              </DialogHeader>
              <DialogFooter>
                <Button variant="ghost" onClick={() => setConfirming(false)} disabled={deleting}>Keep</Button>
                <Button variant="destructive" disabled={deleting} onClick={() => { setDeleting(true); onDelete() }}>
                  {deleting ? 'Deleting…' : 'Delete chat'}
                </Button>
              </DialogFooter>
            </DialogContent>
          </Dialog>
        </>
      )}
    </PageBar>
  )
}
