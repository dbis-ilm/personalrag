// Run with `npm test` (node --test; Node strips the types itself).
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { caretAllows, emptyHistory, navigate, remember } from './questionHistory.ts'

test('Up pages back from the draft, Down returns to it', () => {
  let st = emptyHistory(['dritte', 'zweite', 'erste'])
  let r = navigate(st, 'up', 'mein Entwurf')!
  assert.equal(r.text, 'dritte'); st = r.state
  r = navigate(st, 'up', r.text)!
  assert.equal(r.text, 'zweite'); st = r.state
  r = navigate(st, 'up', r.text)!
  assert.equal(r.text, 'erste'); st = r.state
  assert.equal(navigate(st, 'up', 'erste'), null, 'oldest reached')
  r = navigate(st, 'down', 'erste')!
  assert.equal(r.text, 'zweite'); st = r.state
  r = navigate(st, 'down', 'zweite')!
  assert.equal(r.text, 'dritte'); st = r.state
  r = navigate(st, 'down', 'dritte')!
  assert.equal(r.text, 'mein Entwurf', 'draft restored'); st = r.state
  assert.equal(navigate(st, 'down', 'mein Entwurf'), null)
})

test('an empty history leaves the keys alone', () => {
  assert.equal(navigate(emptyHistory(), 'up', 'x'), null)
  assert.equal(navigate(emptyHistory(['a']), 'down', 'x'), null)
})

test('remember puts the question first, once', () => {
  const st = remember(emptyHistory(['a', 'b', 'c']), 'b')
  assert.deepEqual(st.list, ['b', 'a', 'c'])
  assert.equal(st.index, -1)
  assert.equal(remember(emptyHistory(['a', 'b']), 'c', 2).list.length, 2)
})

test('Up only from the first line, Down only from the last', () => {
  const v = 'Zeile eins\nZeile zwei'
  assert.equal(caretAllows(v, 3, 3, 'up'), true)
  assert.equal(caretAllows(v, 14, 14, 'up'), false)
  assert.equal(caretAllows(v, 14, 14, 'down'), true)
  assert.equal(caretAllows(v, 3, 3, 'down'), false)
})
