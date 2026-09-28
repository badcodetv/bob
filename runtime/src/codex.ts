// The Codex driver: one turn = one runStreamed() on a Codex thread, resumed by thread id. The SDK
// runs the bundled `codex exec` CLI; events are passed through exactly as it emits them. As with
// Claude, everything the turn runs with — model, effort and the system prompt — comes in the turn.
// (turn.tools is Claude's allowedTools and is ignored here: Codex runs with full access.)
//
// Codex signs in with a ChatGPT account, stored as auth.json in $CODEX_HOME on the project's volume
// (Decision 7). Kai runs `codex login --device-auth` in the container once per project; Codex
// refreshes the token itself from then on.
import { existsSync } from 'node:fs';
import { homedir } from 'node:os';
import { join } from 'node:path';
import { Codex, type CodexOptions, type ModelReasoningEffort, type ThreadEvent, type ThreadOptions, type TurnOptions } from '@openai/codex-sdk';
import { turnEnv, type Turn, type TurnResult } from './turn.js';
import { bobMcp } from './mcp.js';

/** The part of the SDK the driver uses, so tests can run a turn without the CLI. */
interface CodexThread {
  runStreamed(input: string, options?: TurnOptions): Promise<{ events: AsyncIterable<ThreadEvent> }>;
}
interface CodexClient {
  startThread(options: ThreadOptions): CodexThread;
  resumeThread(id: string, options: ThreadOptions): CodexThread;
}

export interface CodexDeps {
  codex?: (options: CodexOptions) => CodexClient;
  /** $CODEX_HOME, where auth.json lives. */
  home?: string;
  /** This project's name, for the login command in the not-logged-in error. */
  project?: string;
}

export async function runCodexTurn(turn: Turn, apiUrl: string, emit: (event: unknown) => void, signal: AbortSignal, deps: CodexDeps = {}): Promise<TurnResult> {
  const home = deps.home ?? process.env.CODEX_HOME ?? join(homedir(), '.codex');
  if (!existsSync(join(home, 'auth.json'))) {
    // Without this, codex exec would retry the OpenAI API unauthenticated five times and fail with a 401.
    const project = deps.project ?? process.env.BOB_PROJECT_NAME ?? '<name>';
    return { error: `Codex is not logged in for this project. On the box run: docker exec -it bob-project-${project} codex login --device-auth` };
  }

  const mcp = bobMcp(apiUrl, turn.mcpToken);
  const options: CodexOptions = {
    // The SDK's env replaces the CLI's environment rather than adding to it.
    env: { ...definedEnv(), ...turnEnv(turn) },
    // --config overrides for this run only; nothing is written to $CODEX_HOME/config.toml.
    config: {
      // Bob's prompt (the environment note, the project prompt and the worker prompt, composed by the
      // API) goes on top of Codex's own instructions as developer instructions.
      ...(turn.systemPrompt ? { developer_instructions: turn.systemPrompt } : {}),
      mcp_servers: { bob: { url: mcp.url, http_headers: mcp.headers } },
    },
  };
  const codex = deps.codex ? deps.codex(options) : new Codex(options);
  const threadOptions: ThreadOptions = {
    workingDirectory: turn.cwd,
    // /project/work holds many repositories (or none); it is not one itself.
    skipGitRepoCheck: true,
    // The container is the sandbox, and there is no one to ask for approval mid-turn.
    sandboxMode: 'danger-full-access',
    approvalPolicy: 'never',
    webSearchMode: 'live',
    model: turn.model,
    modelReasoningEffort: turn.effort as ModelReasoningEffort | undefined,
  };
  const thread = turn.resume ? codex.resumeThread(turn.resume, threadOptions) : codex.startThread(threadOptions);

  let harnessSessionId = turn.resume;
  let error: string | undefined;
  try {
    const { events } = await thread.runStreamed(turn.text, { signal });
    for await (const event of events) {
      if (event.type === 'thread.started') harnessSessionId = event.thread_id;
      // An `error` event may be a retry notice ("Reconnecting... 1/5") the turn then recovers from;
      // it only fails the turn if nothing completes after it. turn.failed always does.
      else if (event.type === 'error') error = event.message;
      else if (event.type === 'turn.failed') error = event.error.message;
      else if (event.type === 'turn.completed') error = undefined;
      emit(event);
    }
  } catch (err) {
    // Keep the thread id even when the turn fails, so the next turn resumes the same conversation.
    return { harnessSessionId, error: String((err as Error)?.message ?? err) };
  }
  return error ? { harnessSessionId, error } : { harnessSessionId };
}

// process.env without its unset keys: the SDK's env is a Record<string, string>.
function definedEnv(): Record<string, string> {
  return Object.fromEntries(Object.entries(process.env).filter((e): e is [string, string] => e[1] !== undefined));
}
