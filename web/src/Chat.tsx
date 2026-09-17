import { useEffect, useMemo, useState } from 'react'
import { AssistantRuntimeProvider, useExternalStoreRuntime, type ThreadMessageLike } from '@assistant-ui/react'
import { TooltipProvider } from '@/components/ui/tooltip'
import { Thread, type ThreadComponents } from '@/components/assistant-ui/elements/thread.aui'
import { api, type BobEvent, type Session, type Settings, type Worker } from './api'
import { ChatHeader } from './ChatHeader'
import { claudeDelta, claudeMessages } from './engines/claude'
import { TrailGroup, TrailStep } from './Trail'
import { engineName, prettyModel, WorkerBadge } from './ui'

const trail: ThreadComponents = { ToolGroup: TrailGroup, ToolFallback: TrailStep }

// One converter per engine. An engine without one shows its events raw.
function toMessages(engine: string, events: BobEvent[], live: string): ThreadMessageLike[] {
  if (engine === 'claude') return claudeMessages(events, live)
  return events.map((e) => ({
    id: `e${e.id}`,
    role: e.kind === 'bob.user_message' ? 'user' : 'assistant',
    content: [{ type: 'text', text: e.kind === 'bob.user_message' ? e.payload.text : '```json\n' + JSON.stringify(e.payload, null, 2) + '\n```' }],
  }))
}

export function Chat({ session: initial, worker, onSent, onDeleted }: { session: Session; worker?: Worker; onSent: () => void; onDeleted: () => void }) {
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
    convertMessage: (m) => m,
    onNew: async (m) => {
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

  return (
    <div className="flex h-full flex-col">
      <ChatHeader worker={session.worker} engine={session.engine} title={title} workerDefaults={worker}
        settings={{ model: session.model, effort: session.effort }} onChange={change} onDelete={remove} />
      <div className="min-h-0 flex-1">
        <TooltipProvider>
          <AssistantRuntimeProvider runtime={runtime}>
            <Thread components={trail} placeholder={`Message ${session.worker}…`} />
          </AssistantRuntimeProvider>
        </TooltipProvider>
      </div>
    </div>
  )
}

/**
 * A chat that does not exist yet. Picking a worker opens this; the session is only created
 * when the first message is sent, so browsing workers leaves no empty chats behind.
 */
export function NewChat({ project, worker, loading, onCreated }: { project: string; worker?: Worker; loading: boolean; onCreated: (s: Session) => void }) {
  const [pending, setPending] = useState<string | null>(null)
  const [settings, setSettings] = useState<Settings>({ model: '', effort: '' })
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
        if (!worker) throw new Error('this worker is not in the project any more')
        const session = await api.createSession(project, worker.name, settings)
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
      {worker && <ChatHeader worker={worker.name} engine={worker.engine} workerDefaults={worker} settings={settings} onChange={setSettings} />}
      <div className="min-h-0 flex-1">
        <TooltipProvider>
          <AssistantRuntimeProvider runtime={runtime}>
            <Thread components={{ ...trail, Welcome: () => <Welcome worker={worker} loading={loading} /> }}
              placeholder={worker ? `Message ${worker.name}…` : 'Send a message…'} />
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
        This chat gets its own branch of the project repository.
      </p>
    </div>
  )
}
