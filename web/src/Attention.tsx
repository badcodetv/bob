import { useState } from 'react'
import { Button } from '@/components/ui/button'
import { api, type Attention } from './api'
import { Section } from './Overview'
import { when } from './ui'

/**
 * "Needs you": the project's open requests for a person (request_human_attention). Replying in the
 * chat closes a request on the server; Dismiss closes it without a reply.
 */
export function NeedsYou({ project, requests, onChanged, onError }: {
  project: string
  requests: Attention[]
  onChanged: () => void
  onError: (e: unknown) => void
}) {
  const [busy, setBusy] = useState<string | null>(null)
  const dismiss = (id: string) => {
    setBusy(id)
    api.dismissAttention(id).then(onChanged).catch(onError).finally(() => setBusy(null))
  }
  return (
    <Section title="Needs you" count={requests.length}>
      <ul>
        {requests.map((r) => (
          <li key={r.id} className="flex flex-col gap-1.5 border-t px-1 py-3.5 first:border-t-0">
            <p className="text-[14.5px] whitespace-pre-wrap">{r.message}</p>
            <div className="flex items-center gap-2 text-[12.5px]">
              <span className="text-faint">{r.worker || 'a chat'}, {r.kind === 'notice' ? 'notice, ' : ''}{when(r.created_at)}</span>
              <a href={`#/p/${project}/s/${r.session_id}`} className="text-muted-foreground hover:text-foreground underline underline-offset-2">Open chat</a>
              <Button variant="outline" size="sm" className="ml-auto" disabled={busy === r.id} onClick={() => dismiss(r.id)}>Dismiss</Button>
            </div>
          </li>
        ))}
      </ul>
    </Section>
  )
}
