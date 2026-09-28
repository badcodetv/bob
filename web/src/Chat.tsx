import { useEffect, useMemo, useState } from 'react'
import { AssistantRuntimeProvider, useExternalStoreRuntime, type ThreadMessageLike } from '@assistant-ui/react'
import { TooltipProvider } from '@/components/ui/tooltip'
import { Thread, type ThreadComponents } from '@/components/assistant-ui/elements/thread.aui'
import { api, type BobEvent, type Session, type Settings, type Worker } from './api'
import { ChatHeader } from './ChatHeader'
import { claudeDelta, claudeEfforts, claudeMessages, claudeModels } from './engines/claude'
import { codexEfforts, codexMessages, codexModels } from './engines/codex'
import { TrailGroup, TrailStep } from './Trail'
import { engineName, Menu, MenuItem, MenuNote, prettyModel, WorkerBadge } from './ui'

const trail: ThreadComponents = { ToolGroup: TrailGroup, ToolFallback: TrailStep }

// One converter per engine. An engine without one shows its events raw.
function toMessages(engine: string, events: BobEvent[], live: string): ThreadMessageLike[] {
  if (engine === 'claude') return claudeMessages(events, live)
  if (engine === 'codex') return codexMessages(events, live)
  return events.map((e) => ({
    id: `e${e.id}`,
    role: e.kind === 'bob.user_message' ? 'user' : 'assistant',
    content: [{ type: 'text', text: e.kind === 'bob.user_message' ? e.payload.text : '```json\n' + JSON.stringify(e.payload, null, 2) + '\n```' }],
  }))
}

export function Chat({ session: initial, worker, removed, onSent, onDeleted }: {
  session: Session; worker?: Worker; removed?: boolean; onSent: () => void; onDeleted: () => void
}) {
  const [session, setSession] = useState(initial)
  const [events, setEvents] = useState<BobEvent[]>([])
  const [live, setLive] = useState('')

  useEffect(() => {
    setEvents([])
    setLive('')
    const source = new EventSource(`/api/sessions/${session.id}/stream`)
    source.onmessage = (msg) => {
      const e = JSON.parse(msg.data) as BobEvent
      if (!e.id) {
        // Ephemeral: a streamed token delta.
        if (session.engine === 'claude') setLive((s) => s + claudeDelta(e.payload))
        return
      }
      setLive('')
      setEvents((prev) => (prev.length && prev[prev.length - 1].id >= e.id ? prev : [...prev, e]))
    }
    return () => source.close()
  }, [session.id, session.engine])

  const isRunning = useMemo(() => {
    for (let i = events.length - 1; i >= 0; i--) {
      const k = events[i].kind
      if (k === 'bob.turn_done' || k === 'bob.turn_failed') return false
      if (k === 'bob.user_message') return true
    }
    return false
  }, [events])

  const messages = useMemo(() => toMessages(session.engine, events, live), [session.engine, events, live])

  const runtime = useExternalStoreRuntime<ThreadMessageLike>({
    messages,
    isRunning,
    isDisabled: removed,
    convertMessage: (m) => m,
    onNew: async (m) => {
      if (removed) return
      const text = m.content.map((p) => (p.type === 'text' ? p.text : '')).join('')
      if (!text.trim()) return
      await api.send(session.id, text)
      onSent()
    },
    onCancel: async () => { await api.interrupt(session.id) },
  })

  const title = useMemo(() => events.find((e) => e.kind === 'bob.user_message')?.payload.text as string | undefined, [events])

  const change = (settings: Settings) =>
    api.updateSession(session.id, settings).then(setSession).catch((e) => alert(e.message))
  const remove = () => api.deleteSession(session.id).then(onDeleted).catch((e) => alert(e.message))

  const placeholder = removed
    ? "This chat's worker was deleted. Its history stays readable."
    : session.worker ? `Message ${session.worker}…` : 'Send a message…'

  return (
    <div className="flex h-full flex-col">
      <ChatHeader worker={session.worker} engine={session.engine} title={title} workerDefaults={worker}
        settings={{ model: session.model, effort: session.effort }} onChange={change} onDelete={remove} />
      <div className="min-h-0 flex-1">
        <TooltipProvider>
          <AssistantRuntimeProvider runtime={runtime}>
            <Thread components={trail} placeholder={placeholder} />
          </AssistantRuntimeProvider>
        </TooltipProvider>
      </div>
    </div>
  )
}

/**
 * A chat that does not exist yet. Picking a worker opens this; the session is only created
 * when the first message is sent, so browsing workers leaves no empty chats behind.
 *
 * `plain` (worker === '' in the route) skips the worker entirely: the engine, model and effort
 * are chosen here instead of coming from a worker's own settings.
 */
export function NewChat({ project, worker, plain, loading, onCreated }: {
  project: string; worker?: Worker; plain?: boolean; loading: boolean; onCreated: (s: Session) => void
}) {
  const [pending, setPending] = useState<string | null>(null)
  const [settings, setSettings] = useState<Settings>({ model: '', effort: '' })
  const [engine, setEngine] = useState('claude')
  const messages = useMemo<ThreadMessageLike[]>(
    () => (pending === null ? [] : [{ id: 'pending', role: 'user', content: [{ type: 'text', text: pending }] }]),
    [pending],
  )
  const runtime = useExternalStoreRuntime<ThreadMessageLike>({
    messages,
    isRunning: pending !== null,
    convertMessage: (m) => m,
    onNew: async (m) => {
      const text = m.content.map((p) => (p.type === 'text' ? p.text : '')).join('')
      if (!text.trim()) return
      setPending(text)
      try {
        if (plain) {
          const session = await api.createSession(project, '', engine, settings)
          await api.send(session.id, text)
          onCreated(session)
          return
        }
        if (!worker) throw new Error('this worker is not in the project any more')
        const session = await api.createSession(project, worker.name, worker.engine, settings)
        await api.send(session.id, text)
        onCreated(session)
      } catch (err) {
        setPending(null)
        alert(String((err as Error)?.message ?? err))
      }
    },
  })
  return (
    <div className="flex h-full flex-col">
      {plain
        ? <PlainChatBar engine={engine} settings={settings}
            onEngineChange={(e) => { setEngine(e); setSettings({ model: '', effort: '' }) }} onSettingsChange={setSettings} />
        : worker && <ChatHeader worker={worker.name} engine={worker.engine} workerDefaults={worker} settings={settings} onChange={setSettings} />}
      <div className="min-h-0 flex-1">
        <TooltipProvider>
          <AssistantRuntimeProvider runtime={runtime}>
            <Thread components={{ ...trail, Welcome: () => (plain ? <PlainWelcome /> : <Welcome worker={worker} loading={loading} />) }}
              placeholder={plain ? 'Send a message…' : worker ? `Message ${worker.name}…` : 'Send a message…'} />
          </AssistantRuntimeProvider>
        </TooltipProvider>
      </div>
    </div>
  )
}

function Welcome({ worker, loading }: { worker?: Worker; loading: boolean }) {
  if (!worker) {
    return <p className="text-muted-foreground mb-6 px-2">{loading ? 'Loading workers…' : 'This worker is not in the project any more. Pick another from the sidebar.'}</p>
  }
  return (
    <div className="mb-8 flex max-w-xl flex-col gap-3.5 px-2">
      <WorkerBadge name={worker.name} engine={worker.engine} size="lg" />
      <h1 className="text-3xl leading-tight font-semibold tracking-[-0.025em] text-balance">New chat with {worker.name}</h1>
      <p className="text-muted-foreground border-l-2 py-0.5 pl-3 text-sm line-clamp-4">{worker.prompt}</p>
      <p className="text-faint text-[13px]">
        {engineName(worker.engine)}{worker.model ? `, ${prettyModel(worker.model)}` : ''}{worker.effort ? `, ${worker.effort} effort` : ''}.
      </p>
    </div>
  )
}

function PlainWelcome() {
  return (
    <div className="mb-8 flex max-w-xl flex-col gap-3.5 px-2">
      <h1 className="text-3xl leading-tight font-semibold tracking-[-0.025em] text-balance">New plain chat</h1>
      <p className="text-muted-foreground text-sm">
        No worker: it sees every Bob tool, including creating one. Pick an engine, model and effort above, then say hello.
      </p>
    </div>
  )
}

// Model and effort choices for a plain chat's engine.
const plainOptions: Record<string, { model: string[]; effort: string[] }> = {
  claude: { model: claudeModels, effort: claudeEfforts },
  codex: { model: codexModels, effort: codexEfforts },
}

function PlainChatBar({ engine, settings, onEngineChange, onSettingsChange }: {
  engine: string; settings: Settings; onEngineChange: (engine: string) => void; onSettingsChange: (s: Settings) => void
}) {
  const opts = plainOptions[engine] ?? { model: [], effort: [] }
  return (
    <header className="flex h-13 shrink-0 items-center gap-2.5 border-b px-4 md:pl-6">
      <span className="shrink-0 text-[15px] font-semibold">Plain chat</span>
      <span className="flex-1" />
      <Menu label="Engine for this chat" align="end"
        triggerClassName="hover:bg-accent data-popup-open:bg-accent inline-flex items-baseline gap-1.5 rounded-md px-2 py-1 text-[13px]"
        trigger={<><span className="text-faint hidden text-xs sm:inline">Engine</span><span>{engineName(engine)}</span></>}>
        <MenuNote>Engine for this chat</MenuNote>
        <MenuItem checked={engine === 'claude'} onClick={() => onEngineChange('claude')}>Claude</MenuItem>
        <MenuItem checked={engine === 'codex'} onClick={() => onEngineChange('codex')}>Codex</MenuItem>
      </Menu>
      <Menu label="Model for this chat" align="end"
        triggerClassName="hover:bg-accent data-popup-open:bg-accent inline-flex items-baseline gap-1.5 rounded-md px-2 py-1 text-[13px]"
        trigger={<><span className="text-faint hidden text-xs sm:inline">Model</span><span>{settings.model ? prettyModel(settings.model) : 'Default'}</span></>}>
        <MenuNote>Model for this chat</MenuNote>
        {opts.model.map((m) => (
          <MenuItem key={m} checked={settings.model === m} onClick={() => onSettingsChange({ ...settings, model: m })}>{prettyModel(m)}</MenuItem>
        ))}
      </Menu>
      <Menu label="Effort for this chat" align="end"
        triggerClassName="hover:bg-accent data-popup-open:bg-accent inline-flex items-baseline gap-1.5 rounded-md px-2 py-1 text-[13px]"
        trigger={<><span className="text-faint hidden text-xs sm:inline">Effort</span><span>{settings.effort || 'Default'}</span></>}>
        <MenuNote>Effort for this chat</MenuNote>
        {opts.effort.map((e) => (
          <MenuItem key={e} checked={settings.effort === e} onClick={() => onSettingsChange({ ...settings, effort: e })}>{e}</MenuItem>
        ))}
      </Menu>
    </header>
  )
}
