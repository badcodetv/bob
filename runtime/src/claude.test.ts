import { test } from 'node:test';
import assert from 'node:assert/strict';
import { claudeSettings } from './claude.js';

test('every Claude turn has the Workflow tool on, sized large', () => {
  assert.equal(claudeSettings.enableWorkflows, true);
  assert.equal(claudeSettings.workflowSizeGuideline, 'large');
});
