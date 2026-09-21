import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { loadProject, NO_PROJECT, parseProject } from './project.js';

test('front matter is settings, the body is the preamble', () => {
  const p = parseProject('---\nfiles_root: /site/\ndefault_model: opus\n---\nWolf tracks hypotheses.\n\n## Labels\nhypothesis=<slug>\n');
  assert.equal(p.filesRoot, 'site');
  assert.equal(p.defaultModel, 'opus');
  assert.match(p.preamble, /^Wolf tracks hypotheses\./);
  assert.match(p.preamble, /hypothesis=<slug>$/);
});

test('a bob.md that is only prose is a good bob.md', () => {
  const p = parseProject('Just the goal.\n');
  assert.deepEqual(p, { filesRoot: '', preamble: 'Just the goal.' });
});

test('files_root cannot leave the repository', () => {
  assert.throws(() => parseProject('---\nfiles_root: ../etc\n---\n'), /inside the repository/);
});

test('a project without a bob.md has no preamble, and that is not an error', async () => {
  const dir = await mkdtemp(join(tmpdir(), 'bob-project-'));
  assert.deepEqual(await loadProject(dir), NO_PROJECT);
  await writeFile(join(dir, 'bob.md'), '---\nfiles_root: site\n---\nHello.');
  assert.deepEqual(await loadProject(dir), { filesRoot: 'site', defaultModel: undefined, preamble: 'Hello.' });
});
