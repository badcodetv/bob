import { test } from 'node:test'
import assert from 'node:assert/strict'
import * as auth from './auth.js'
import { checkAuth } from './auth.js'

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

test('the password is BOB_RUNTIME_TOKEN itself, compared as given', () => {
  // What `openssl rand -hex 32` makes, and what Bob sends as bob:<token>.
  const token = '9f2c4e7a1b3d5f6071829304a5b6c7d8e9f00112233445566778899aabbccddee'
  assert.equal(checkAuth(basic(`bob:${token}`), token), true)
  assert.equal(checkAuth(basic(`bob:${token.toUpperCase()}`), token), false, 'no normalising')
  assert.equal(checkAuth(basic('bob:a:b'), 'a:b'), true, 'only the first colon splits user from password')
  // Nothing derives the password any more: each project's token is its own random value.
  assert.equal('runtimeToken' in auth, false)
})
