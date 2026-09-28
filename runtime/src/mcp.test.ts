import { test } from 'node:test'
import assert from 'node:assert/strict'
import { bobMcp } from './mcp.js'

test('bobMcp points at the API\'s /mcp with the chat\'s bearer token', () => {
  assert.deepEqual(bobMcp('http://api:8070', 'sess-1.abcdef'), {
    url: 'http://api:8070/mcp',
    headers: { Authorization: 'Bearer sess-1.abcdef' },
  })
})
