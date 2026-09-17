// A worker is one markdown file in the project's git folder: YAML front matter for the
// settings, the body for the system prompt. workers/<name>.md → worker "<name>".
import { readdir, readFile } from 'node:fs/promises';
import { join } from 'node:path';
import { parse } from 'yaml';

export type Engine = 'claude' | 'codex' | 'opencode';
export const ENGINES: Engine[] = ['claude', 'codex', 'opencode'];

export interface Worker {
  name: string;
  engine: Engine;
  model?: string;
  effort?: string;
  /** Tools the worker may use without asking. Omitted = the harness default. */
  tools?: string[];
  prompt: string;
}

export function parseWorker(name: string, text: string): Worker {
  const m = /^---\r?\n([\s\S]*?)\r?\n---\r?\n?([\s\S]*)$/.exec(text);
  if (!m) throw new Error(`worker ${name}: missing front matter (--- … ---)`);
  const meta = (parse(m[1]) ?? {}) as Record<string, unknown>;
  const engine = meta.engine as Engine;
  if (!ENGINES.includes(engine)) {
    throw new Error(`worker ${name}: engine must be one of ${ENGINES.join(', ')}, got ${JSON.stringify(meta.engine)}`);
  }
  if (meta.tools !== undefined && !(Array.isArray(meta.tools) && meta.tools.every((t) => typeof t === 'string'))) {
    throw new Error(`worker ${name}: tools must be a list of strings`);
  }
  return {
    name,
    engine,
    model: optString(meta.model),
    effort: optString(meta.effort),
    tools: meta.tools as string[] | undefined,
    prompt: m[2].trim(),
  };
}

export async function loadWorkers(dir: string): Promise<Worker[]> {
  let files: string[];
  try {
    files = (await readdir(dir)).filter((f) => f.endsWith('.md')).sort();
  } catch (err) {
    if ((err as NodeJS.ErrnoException).code === 'ENOENT') return [];
    throw err;
  }
  return Promise.all(files.map(async (f) => parseWorker(f.slice(0, -3), await readFile(join(dir, f), 'utf8'))));
}

function optString(v: unknown): string | undefined {
  return v === undefined || v === null ? undefined : String(v);
}
