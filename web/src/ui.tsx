// Small pieces shared by the sidebar, the overview and the chat header.
import { createContext, useContext, type ReactNode } from 'react'
import { Menu as MenuPrimitive } from '@base-ui/react/menu'
import { CheckIcon, MenuIcon } from 'lucide-react'
import { cn } from '@/lib/utils'

export const engineNames: Record<string, string> = { claude: 'Claude', codex: 'Codex', opencode: 'OpenCode' }
export const engineName = (engine: string) => engineNames[engine] ?? engine

const badgeColours: Record<string, string> = {
  claude: 'bg-engine-claude',
  codex: 'bg-engine-codex',
  opencode: 'bg-engine-opencode',
}

/** A worker's letter tile, coloured by its engine. */
export function WorkerBadge({ name, engine, size = 'sm' }: { name: string; engine: string; size?: 'sm' | 'lg' }) {
  return (
    <span aria-hidden className={cn(
      'text-badge-ink grid shrink-0 place-items-center font-bold',
      badgeColours[engine] ?? 'bg-muted-foreground',
      size === 'sm' ? 'size-[22px] rounded-md text-xs' : 'size-11 rounded-[11px] text-[22px]',
    )}>
      {name.slice(0, 1).toUpperCase()}
    </span>
  )
}

/** claude-sonnet-5 → Sonnet 5, claude-fable-5-1 → Fable 5.1. Unknown shapes pass through. */
export function prettyModel(model: string) {
  const m = model.match(/^claude-([a-z]+)-(\d+)(?:-(\d+))?$/)
  if (!m) return model
  return `${m[1][0].toUpperCase()}${m[1].slice(1)} ${m[2]}${m[3] ? `.${m[3]}` : ''}`
}

/** Today → 14:46, this week → Tue, older → 12 Sep. */
export function when(iso?: string) {
  if (!iso) return ''
  const d = new Date(iso)
  const now = new Date()
  if (d.toDateString() === now.toDateString()) return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', hourCycle: 'h23' })
  if (now.getTime() - d.getTime() < 6 * 86400_000) return d.toLocaleDateString([], { weekday: 'short' })
  return d.toLocaleDateString([], { day: 'numeric', month: 'short' })
}

/** How long ago, for sync status: just now, 2 min ago, 3 h ago, or a date. */
export function ago(date?: Date) {
  if (!date) return ''
  const s = Math.round((Date.now() - date.getTime()) / 1000)
  if (s < 45) return 'just now'
  if (s < 3600) return `${Math.round(s / 60)} min ago`
  if (s < 86400) return `${Math.round(s / 3600)} h ago`
  return when(date.toISOString())
}

// The drawer: below md the sidebar slides in, opened by a menu button in each page's header.
export const DrawerContext = createContext<() => void>(() => {})

export function DrawerButton() {
  const open = useContext(DrawerContext)
  return (
    <button type="button" onClick={open} aria-label="Show projects and workers"
      className="text-muted-foreground hover:bg-accent hover:text-foreground -ml-2 grid size-8 place-items-center rounded-lg md:hidden">
      <MenuIcon className="size-5" />
    </button>
  )
}

/** The header bar every page shares. */
export function PageBar({ children }: { children: ReactNode }) {
  return (
    <header className="flex h-13 shrink-0 items-center gap-2.5 border-b px-4 md:pl-6">
      <DrawerButton />
      {children}
    </header>
  )
}

export function Menu({ trigger, triggerClassName, label, align = 'start', children }: {
  trigger: ReactNode; triggerClassName?: string; label: string; align?: 'start' | 'end'; children: ReactNode
}) {
  return (
    <MenuPrimitive.Root>
      <MenuPrimitive.Trigger aria-label={label} className={triggerClassName}>{trigger}</MenuPrimitive.Trigger>
      <MenuPrimitive.Portal>
        <MenuPrimitive.Positioner sideOffset={6} align={align} className="z-50">
          <MenuPrimitive.Popup className="bg-popover text-popover-foreground min-w-56 rounded-[10px] border p-1.5 shadow-[0_8px_24px_rgba(27,31,36,.12),0_1px_2px_rgba(27,31,36,.08)] outline-none">
            {children}
          </MenuPrimitive.Popup>
        </MenuPrimitive.Positioner>
      </MenuPrimitive.Portal>
    </MenuPrimitive.Root>
  )
}

export function MenuItem({ onClick, checked, hint, danger, children }: {
  onClick: () => void; checked?: boolean; hint?: string; danger?: boolean; children: ReactNode
}) {
  return (
    <MenuPrimitive.Item onClick={onClick} className={cn(
      'data-highlighted:bg-accent flex cursor-pointer items-center gap-2.5 rounded-md px-2.5 py-1.5 text-[13.5px] outline-none select-none',
      danger && 'text-destructive',
    )}>
      {checked !== undefined && <span className="text-cobalt w-3.5 shrink-0">{checked && <CheckIcon className="size-3.5" strokeWidth={2.6} />}</span>}
      <span className="min-w-0 flex-1 truncate">{children}</span>
      {hint && <span className="text-faint ml-3 text-xs">{hint}</span>}
    </MenuPrimitive.Item>
  )
}

export function MenuNote({ children }: { children: ReactNode }) {
  return <div className="text-muted-foreground px-2.5 pt-1.5 pb-1 text-[12.5px]">{children}</div>
}

export function MenuSeparator() {
  return <MenuPrimitive.Separator className="bg-border mx-0.5 my-1 h-px" />
}
