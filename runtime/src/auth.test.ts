import { test } from 'node:test'
import assert from 'node:assert/strict'
import { checkAuth, runtimeToken } from './auth.js'

const basic = (s: string) => 'Basic ' + Buffer.from(s).toString('base64')

test('only basic auth with the token passes', () => {
  assert.equal(checkAuth(basic('bob:t0ken'), 't0ken'), true)
  assert.equal(checkAuth(basic('anyone:t0ken'), 't0ken'), true)
  assert.equal(checkAuth(basic('bob:wrong'), 't0ken'), false)
  assert.equal(checkAuth(basic('bob:t0ken2'), 't0ken'), false)
  assert.equal(checkAuth(basic('t0ken'), 't0ken'), false)
  assert.equal(checkAuth('Bearer t0ken', 't0ken'), false)
  assert.equal(checkAuth(undefined, 't0ken'), false)
  assert.equal(checkAuth(basic('bob:'), ''), false, 'an empty token never matches')
})

test('the project password matches what Bob derives, and differs per project', () => {
  // Fixed vector: api/internal/runtime.Token([]byte("testkey1234567890"), "bob-examples").
  assert.equal(runtimeToken('testkey1234567890', 'bob-examples'),
    'cca51a8a1d298490b7edcbf4018c46b0d15e10c5b6b11e7a5852c2f918b03543');
  assert.notEqual(runtimeToken('k', 'a'), runtimeToken('k', 'b'));
});
