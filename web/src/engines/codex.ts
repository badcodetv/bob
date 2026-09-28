// Codex's native events → assistant-ui messages. Each engine gets its own converter;
// there is no shared event type (DESIGN.md §4).
import type { ThreadMessageLike } from '@assistant-ui/react'
import type { BobEvent } from '../api'

// `codex debug models` in bob-project-dev (codex-cli 0.158.0), the ids with visibility "list"
// (the ones Codex itself offers a person): gpt-6-astra, gpt-6-sol, gpt-6-luna, gpt-5.6-sol,
// gpt-5.6-terra, gpt-5.6-luna and gpt-5.5. ("hide" ids — gpt-daybreak-*-latest, codex-auto-review —
// are left out, same as Codex's own picker.)
export const codexModels = ['gpt-6-astra', 'gpt-6-sol', 'gpt-6-luna', 'gpt-5.6-sol', 'gpt-5.6-terra', 'gpt-5.6-luna', 'gpt-5.5']
/** Effort levels offered when choosing a Codex chat's settings (workers.go's codexEfforts). */
export const codexEfforts = ['minimal', 'low', 'medium', 'high', 'xhigh']

type Part = Exclude<ThreadMessageLike['content'], string>[number]
type ToolCall = Extract<Part, { type: 'tool-call' }>

export function codexMessages(events: BobEvent[], live: string): ThreadMessageLike[] {
  const out: ThreadMessageLike[] = []
  let assistant: { id: string; content: Part[] } | null = null
  const tools = new Map<string, ToolCall>()
  // A turn's reply keeps one id from its first item to its last stored event, named after the
  // user message that started the turn (same reasoning as claude.ts's turnId).
  let turnId = 'start'

  const openAssistant = () => {
    if (!assistant) {
      const id = `reply-${turnId}`
      assistant = { id, content: [] }
      out.push({ id, role: 'assistant', content: assistant.content })
    }
    return assistant
  }

  const toolPart = (id: string, name: string, args: unknown): ToolCall => {
    let call = tools.get(id)
    if (!call) {
      call = { type: 'tool-call', toolCallId: id, toolName: name, args: (args ?? {}) as any }
      tools.set(id, call)
      openAssistant().content.push(call)
    }
    return call
  }

  for (const e of events) {
    const p = e.payload

    switch (e.kind) {
      case 'bob.user_message':
        assistant = null
        turnId = String(e.id)
        out.push({ id: `e${e.id}`, role: 'user', content: [{ type: 'text', text: p.text }] })
        break
      case 'item.completed': {
        const item = p.item
        switch (item?.type) {
          case 'agent_message':
            openAssistant().content.push({ type: 'text', text: item.text })
            break
          case 'reasoning':
            openAssistant().content.push({ type: 'reasoning', text: item.text })
            break
          case 'command_execution': {
            const call = toolPart(item.id, 'command_execution', { command: item.command })
            ;(call as any).result = item.aggregated_output
            ;(call as any).isError = item.status === 'failed'
            break
          }
          case 'mcp_tool_call': {
            const call = toolPart(item.id, `${item.server}.${item.tool}`, item.arguments)
            ;(call as any).result = item.error ? item.error.message : toolResultText(item.result?.content)
            ;(call as any).isError = item.status === 'failed'
            break
          }
          case 'file_change': {
            const call = toolPart(item.id, 'file_change', { changes: item.changes })
            ;(call as any).result = item.changes.map((c: any) => `${c.kind} ${c.path}`).join('\n')
            ;(call as any).isError = item.status === 'failed'
            break
          }
          case 'web_search': {
            const call = toolPart(item.id, 'web_search', { query: item.query })
            ;(call as any).result = ''
            break
          }
          case 'todo_list':
            openAssistant().content.push({
              type: 'text',
              text: (item.items ?? []).map((t: any) => `${t.completed ? '[x]' : '[ ]'} ${t.text}`).join('\n'),
            })
            break
          case 'error':
            openAssistant().content.push({ type: 'text', text: `⚠️ ${item.message}` })
            break
        }
        break
      }
      case 'error':
        openAssistant().content.push({ type: 'text', text: `⚠️ ${p.message}` })
        break
      case 'turn.failed':
        openAssistant().content.push({ type: 'text', text: `⚠️ ${p.error?.message}` })
        break
      case 'bob.turn_failed':
        openAssistant().content.push({ type: 'text', text: `⚠️ ${p.error}` })
        break
    }
  }
  if (live) openAssistant().content.push({ type: 'text', text: live })
  return out
}

function toolResultText(content: unknown): string {
  if (typeof content === 'string') return content
  if (Array.isArray(content)) return content.map((c: any) => (c.type === 'text' ? c.text : `[${c.type}]`)).join('\n')
  return content === undefined ? '' : JSON.stringify(content)
}
