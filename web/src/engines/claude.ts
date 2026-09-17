// Claude's native events → assistant-ui messages. Each engine gets its own converter;
// there is no shared event type (DESIGN.md §4).
import type { ThreadMessageLike } from '@assistant-ui/react'
import type { BobEvent } from '../api'

/** Models and effort levels offered when choosing a Claude chat's settings. */
export const claudeModels = ['claude-fable-5-1', 'claude-opus-5', 'claude-sonnet-5', 'claude-haiku-4-5']
export const claudeEfforts = ['low', 'medium', 'high', 'xhigh', 'max']

type Part = Exclude<ThreadMessageLike['content'], string>[number]
type ToolCall = Extract<Part, { type: 'tool-call' }>

export function claudeMessages(events: BobEvent[], live: string): ThreadMessageLike[] {
  const out: ThreadMessageLike[] = []
  let assistant: { id: string; content: Part[] } | null = null
  const tools = new Map<string, ToolCall>()
  // A turn's reply keeps one id from its first streamed token to its last stored event, named
  // after the user message that started the turn. A changing id makes assistant-ui treat the
  // reply as a new branch ("2 / 2") and remount it (the flicker).
  let turnId = 'start'

  const openAssistant = () => {
    if (!assistant) {
      const id = `reply-${turnId}`
      assistant = { id, content: [] }
      out.push({ id, role: 'assistant', content: assistant.content })
    }
    return assistant
  }

  for (const e of events) {
    const p = e.payload
    // Sub-agent events carry the id of the Task call that started them. For now they are
    // folded away; the Task call itself shows the sub-agent's prompt and final result.
    if (p?.parent_tool_use_id) continue

    switch (e.kind) {
      case 'bob.user_message':
        assistant = null
        turnId = String(e.id)
        out.push({ id: `e${e.id}`, role: 'user', content: [{ type: 'text', text: p.text }] })
        break
      case 'assistant':
        for (const block of p.message?.content ?? []) {
          const a = openAssistant()
          if (block.type === 'text') a.content.push({ type: 'text', text: block.text })
          else if (block.type === 'thinking' && block.thinking) a.content.push({ type: 'reasoning', text: block.thinking })
          else if (block.type === 'tool_use') {
            const call: ToolCall = { type: 'tool-call', toolCallId: block.id, toolName: block.name, args: block.input ?? {} }
            tools.set(block.id, call)
            a.content.push(call)
          }
        }
        break
      case 'user':
        // Tool results come back as "user" events; anything else here is harness bookkeeping.
        for (const block of Array.isArray(p.message?.content) ? p.message.content : []) {
          if (block.type !== 'tool_result') continue
          const call = tools.get(block.tool_use_id)
          if (!call) continue
          ;(call as any).result = toolResultText(block.content)
          ;(call as any).isError = Boolean(block.is_error)
        }
        break
      case 'bob.turn_failed':
        openAssistant().content.push({ type: 'text', text: `⚠️ ${p.error}` })
        break
    }
  }
  if (live) openAssistant().content.push({ type: 'text', text: live })
  return out
}

/** Accumulates streamed text deltas; reset whenever a complete assistant event arrives. */
export function claudeDelta(event: any): string {
  if (event?.type !== 'stream_event') return ''
  const d = event.event
  return d?.type === 'content_block_delta' && d.delta?.type === 'text_delta' ? d.delta.text : ''
}

function toolResultText(content: unknown): string {
  if (typeof content === 'string') return content
  if (Array.isArray(content)) return content.map((c: any) => (c.type === 'text' ? c.text : `[${c.type}]`)).join('\n')
  return JSON.stringify(content)
}
