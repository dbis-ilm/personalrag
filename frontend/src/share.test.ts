import { test } from 'node:test'
import assert from 'node:assert/strict'
import { citedSources, toHTML, toMarkdown, toPlain, type ShareInput } from './share.ts'

const input: ShareInput = {
  question: 'Wie lange ist die Kündigungsfrist?',
  answer: 'Die Frist beträgt **drei Monate** [2].\n\n- zum Monatsende [2]\n- schriftlich [3]',
  sources: [
    { n: 1, file: 'Hausordnung.pdf', location: 'Seite 1', short: 'Ruhezeiten' },
    { n: 2, file: 'Mietvertrag.pdf', location: 'Seite 4', short: 'drei Monate zum Ende' },
    { n: 3, file: 'Mietvertrag.pdf', location: 'Seite 5', short: 'Schriftform' },
  ],
  labels: { question: 'Frage', sources: 'Quellen', footer: 'Erstellt mit PersonalRAG am 27.11.2026' },
}

test('only cited sources, in number order; all when none is cited', () => {
  assert.deepEqual(citedSources(input.answer, input.sources).map((s) => s.n), [2, 3])
  assert.deepEqual(citedSources('ohne Belege', input.sources).map((s) => s.n), [1, 2, 3])
})

test('markdown keeps formatting and lists the sources', () => {
  const md = toMarkdown(input)
  assert.match(md, /^\*\*Frage:\*\* Wie lange/)
  assert.match(md, /\*\*drei Monate\*\* \[2\]/)
  assert.match(md, /### Quellen\n\n- \[2\] Mietvertrag\.pdf, Seite 4 – „drei Monate zum Ende“\n- \[3\]/)
  assert.doesNotMatch(md, /Hausordnung/)
  assert.match(md, /_Erstellt mit PersonalRAG am 27\.11\.2026_\n$/)
})

test('plain text drops the bold markers', () => {
  const txt = toPlain(input)
  assert.match(txt, /Die Frist beträgt drei Monate \[2\]\./)
  assert.doesNotMatch(txt, /\*\*/)
  assert.match(txt, /Quellen\n\[2\] Mietvertrag\.pdf, Seite 4/)
})

test('HTML escapes the model text and renders lists', () => {
  const html = toHTML({ ...input, answer: 'Achtung <script>alert(1)</script> & **fett**\n\n- a\n- b' })
  assert.doesNotMatch(html, /<script>/)
  assert.match(html, /&lt;script&gt;alert\(1\)&lt;\/script&gt; &amp; <strong>fett<\/strong>/)
  assert.match(html, /<ul><li>a<\/li><li>b<\/li><\/ul>/)
  assert.match(html, /<h3>Quellen<\/h3>/)
})
