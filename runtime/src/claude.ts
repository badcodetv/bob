// The Claude driver: one turn = one query() against the Agent SDK, resumed by session id.
// Events are passed through exactly as the SDK emits them. Everything the turn runs with — model,
// effort, tools and the system prompt — comes in the turn: Bob's API decided it.
import { query, type EffortLevel } from '@anthropic-ai/claude-agent-sdk';
import { turnEnv, type Turn, type TurnResult } from './turn.js';
import { bobMcp } from './mcp.js';

export async function runClaudeTurn(turn: Turn, apiUrl: string, emit: (event: unknown) => void, signal: AbortSignal): Promise<TurnResult> {
  const abortController = new AbortController();
  signal.addEventListener('abort', () => abortController.abort(), { once: true });

  let harnessSessionId = turn.resume;
  try {
  for await (const msg of query({
    prompt: turn.text,
    options: {
      cwd: turn.cwd,
      env: { ...process.env, ...turnEnv(turn) },
      resume: turn.resume,
      model: turn.model,
      effort: turn.effort as EffortLevel | undefined,
      // Claude Code's own prompt first, then Bob's: the environment note, the project prompt and the
      // worker prompt, composed by the API.
      systemPrompt: { type: 'preset', preset: 'claude_code', append: turn.systemPrompt },
      allowedTools: turn.tools,
      mcpServers: { bob: { type: 'http', ...bobMcp(apiUrl, turn.mcpToken) } },
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
