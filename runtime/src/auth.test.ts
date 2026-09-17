import { test } from 'node:test'
import assert from 'node:assert/strict'
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
