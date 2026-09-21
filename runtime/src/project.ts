// bob.md is the project's own file, beside workers/ in its config folder. The front matter holds
// settings Bob reads; the body is the project's goal and its labelling scheme, and is appended to
// every worker's system prompt — so what the project is, and how its workers coordinate, is
// written once, by the project, in git.
import { readFile } from 'node:fs/promises';
import { join } from 'node:path';
import { parse } from 'yaml';

export interface Project {
  /** The folder the Files page opens on; empty = the repository root. */
  filesRoot: string;
  /** Overrides the worker's model when the worker names none. */
  defaultModel?: string;
  /** The body: appended to every worker's system prompt. Empty when there is no bob.md. */
  preamble: string;
}

export const NO_PROJECT: Project = { filesRoot: '', preamble: '' };

export function parseProject(text: string): Project {
  const m = /^---\r?\n([\s\S]*?)\r?\n---\r?\n?([\s\S]*)$/.exec(text);
  // Front matter is optional here, unlike a worker: a bob.md that is only prose is a good bob.md.
  if (!m) return { ...NO_PROJECT, preamble: text.trim() };
  const meta = (parse(m[1]) ?? {}) as Record<string, unknown>;
  const filesRoot = String(meta.files_root ?? '').replace(/^\/+|\/+$/g, '');
  if (filesRoot.split('/').includes('..')) {
    throw new Error('bob.md: files_root must be a folder inside the repository');
  }
  return {
    filesRoot,
    defaultModel: meta.default_model === undefined || meta.default_model === null ? undefined : String(meta.default_model),
    preamble: m[2].trim(),
  };
}

/** Reads <configDir>/bob.md. A project without one is not an error: it just has no preamble. */
export async function loadProject(configDir: string): Promise<Project> {
  let text: string;
  try {
    text = await readFile(join(configDir, 'bob.md'), 'utf8');
  } catch (err) {
    if ((err as NodeJS.ErrnoException).code === 'ENOENT') return NO_PROJECT;
    throw err;
  }
  return parseProject(text);
}
