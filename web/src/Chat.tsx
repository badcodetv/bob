import { useEffect, useMemo, useState } from 'react'
import { AssistantRuntimeProvider, useExternalStoreRuntime, type ThreadMessageLike } from '@assistant-ui/react'
import { TooltipProvider } from '@/components/ui/tooltip'
import { Thread } from '@/components/assistant-ui/elements/thread.aui'
import { api, type BobEvent, type Session } from './api'
import { claudeDelta, claudeMessages } from './engines/claude'

// One converter per engine. An engine without one shows its events raw.
function toMessages(engine: string, events: BobEvent[], live: string): ThreadMessageLike[] {
  if (engine === 'claude') return claudeMessages(events, live)
  return events.map((e) => ({
    id: `e${e.id}`,
    role: e.kind === 'bob.user_message' ? 'user' : 'assistant',
    content: [{ type: 'text', text: e.kind === 'bob.user_message' ? e.payload.text : '```json\n' + JSON.stringify(e.payload, null, 2) + '\n```' }],
  }))
}

export function Chat({ session }: { session: Session }) {
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
      if (text.trim()) await api.send(session.id, text)
    },
    onCancel: async () => { await api.interrupt(session.id) },
  })

  return (
    <TooltipProvider>
      <AssistantRuntimeProvider runtime={runtime}>
        <Thread />
      </AssistantRuntimeProvider>
    </TooltipProvider>
  )
}
