// The Claude driver: one turn = one query() against the Agent SDK, resumed by session id.
// Events are passed through exactly as the SDK emits them.
import { query, type EffortLevel } from '@anthropic-ai/claude-agent-sdk';
import type { Worker } from './workers.js';
import type { Turn, TurnResult } from './turn.js';

export async function runClaudeTurn(worker: Worker, turn: Turn, emit: (event: unknown) => void, signal: AbortSignal): Promise<TurnResult> {
  const abortController = new AbortController();
  signal.addEventListener('abort', () => abortController.abort(), { once: true });

  let harnessSessionId = turn.resume;
  try {
  for await (const msg of query({
    prompt: turn.text,
    options: {
      cwd: turn.cwd,
      resume: turn.resume,
      model: turn.model || worker.model,
      effort: (turn.effort || worker.effort) as EffortLevel | undefined,
      systemPrompt: { type: 'preset', preset: 'claude_code', append: worker.prompt },
      allowedTools: worker.tools,
      permissionMode: 'bypassPermissions',
      allowDangerouslySkipPermissions: true,
      includePartialMessages: true,
      skills: 'all',
      abortController,
    },
  })) {
    if (msg.type === 'system' && msg.subtype === 'init') harnessSessionId = msg.session_id;
    emit(msg);
  }
  } catch (err) {
    // Keep the session id even when the turn fails, so the next turn resumes the same conversation.
    return { harnessSessionId, error: String((err as Error)?.message ?? err) };
  }
  return { harnessSessionId };
}
