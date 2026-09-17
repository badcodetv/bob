// The tool trail: a reply's tool calls as a dotted rail of steps, each showing what it actually
// ran, with its output one click away.
import { useState, type PropsWithChildren } from 'react'
import type { ToolCallMessagePartComponent } from '@assistant-ui/react'
import { ChevronRightIcon } from 'lucide-react'
import { cn } from '@/lib/utils'

const MAX_OUTPUT = 8000

export function TrailGroup({ children }: PropsWithChildren) {
  return (
    <ol className="before:border-border relative my-2 flex flex-col before:absolute before:top-2.5 before:bottom-2.5 before:left-[5px] before:border-l-2 before:border-dotted before:content-['']">
      {children}
    </ol>
  )
}

/** One line that says what a tool call did, in the tool's own terms. */
function summary(toolName: string, args: Record<string, any>, argsText?: string): { tool: string; detail: string } {
  const mcp = toolName.match(/^mcp__(.+?)__(.+)$/)
  const tool = mcp ? mcp[2] : toolName
  const pick = (...keys: string[]) => keys.map((k) => args?.[k]).find((v) => typeof v === 'string' && v)
  const detail =
    pick('command', 'file_path', 'notebook_path', 'path', 'pattern', 'url', 'query', 'description', 'skill', 'prompt')
    ?? Object.values(args ?? {}).find((v): v is string => typeof v === 'string')
    ?? argsText ?? ''
  return { tool, detail: detail.replace(/\s+/g, ' ').trim() }
}

export const TrailStep: ToolCallMessagePartComponent = ({ toolName, args, argsText, result, isError, status }) => {
  const [open, setOpen] = useState(false)
  const { tool, detail } = summary(toolName, args as Record<string, any>, argsText)
  const running = status?.type === 'running' && result === undefined
  const failed = Boolean(isError) || (status?.type === 'incomplete' && status.reason === 'error')
  const unfinished = !running && result === undefined
  const output = typeof result === 'string' ? result : result === undefined ? '' : JSON.stringify(result, null, 2)
  const input = toolName === 'Bash' ? String((args as any)?.command ?? '') : JSON.stringify(args, null, 2)

  return (
    <li className="relative">
      <button type="button" onClick={() => setOpen(!open)} aria-expanded={open}
        className="group flex w-full items-center gap-2.5 rounded-md py-1 pr-2 text-left">
        <span className={cn(
          'relative z-10 size-3 shrink-0 rounded-full border-2',
          running ? 'border-cobalt bg-cobalt-soft animate-[bob-pulse_1.2s_ease-in-out_infinite]'
            : failed ? 'border-destructive bg-destructive'
            : unfinished ? 'border-faint bg-background'
            : 'border-faint bg-faint',
        )} />
        <span className="text-faint shrink-0 text-xs">{tool}</span>
        <span className="text-muted-foreground group-hover:text-foreground min-w-0 truncate font-mono text-[13px]">{detail}</span>
        <span className={cn('ml-auto shrink-0 text-xs', running ? 'text-cobalt' : failed ? 'text-destructive' : 'text-faint')}>
          {running ? 'running' : failed ? 'failed' : unfinished ? 'stopped' : ''}
        </span>
        <ChevronRightIcon className={cn('text-faint size-3 shrink-0 transition-transform', open && 'rotate-90')} strokeWidth={2.4} />
      </button>
      {open && (
        <div className="bg-code mt-0.5 mb-1.5 ml-[22px] flex flex-col gap-2 overflow-hidden rounded-lg px-3 py-2.5">
          {input && <pre className="text-muted-foreground overflow-x-auto font-mono text-[12.5px] leading-normal whitespace-pre-wrap">{input}</pre>}
          {output && <pre className={cn('border-border overflow-x-auto border-t pt-2 font-mono text-[12.5px] leading-normal', failed && 'text-destructive')}>
            {output.length > MAX_OUTPUT ? `${output.slice(0, MAX_OUTPUT)}\n… ${output.length - MAX_OUTPUT} more characters` : output}
          </pre>}
          {!output && <span className="text-faint text-xs">{running ? 'Waiting for output…' : 'No output.'}</span>}
        </div>
      )}
    </li>
  )
}
