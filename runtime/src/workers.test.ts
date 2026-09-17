import { test } from 'node:test';
import assert from 'node:assert/strict';
import { parseWorker } from './workers.js';

test('parses front matter and body', () => {
  const w = parseWorker('writer', '---\nengine: claude\nmodel: claude-sonnet-5\neffort: low\ntools: [Read, Write]\n---\nYou write.\n');
  assert.deepEqual(w, { name: 'writer', engine: 'claude', model: 'claude-sonnet-5', effort: 'low', tools: ['Read', 'Write'], prompt: 'You write.' });
});

test('rejects an unknown engine', () => {
  assert.throws(() => parseWorker('x', '---\nengine: gpt\n---\nhi'), /engine must be one of/);
});

test('rejects a file without front matter', () => {
  assert.throws(() => parseWorker('x', 'just a prompt'), /missing front matter/);
});
