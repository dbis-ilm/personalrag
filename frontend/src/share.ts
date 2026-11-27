// Turns an answer with its sources into text to share: Markdown (file),
// plain text (e-mail, clipboard) and HTML (clipboard, print). Sources are
// named by file name, page and a short excerpt; folders stay out, since
// paths reveal folder structure and user names.

export type ShareSource = { n: number; file: string; location: string; short: string }

export type ShareLabels = {
  question: string // "Frage"
  sources: string // "Quellen"
  footer: string // "Erstellt mit PersonalRAG am 27.11.2026"
}

export type ShareInput = { question: string; answer: string; sources: ShareSource[]; labels: ShareLabels }

const citation = /\[(\d+)\]/g

// citedSources are the sources the answer cites, in number order; an
// answer without citations shares all its sources.
export function citedSources(answer: string, sources: ShareSource[]): ShareSource[] {
  const cited = new Set([...answer.matchAll(citation)].map((m) => Number(m[1])))
  const used = sources.filter((s) => cited.has(s.n))
  return (used.length ? used : sources).slice().sort((a, b) => a.n - b.n)
}

function sourceLine(s: ShareSource): string {
  const where = [s.file, s.location].filter(Boolean).join(', ')
  const excerpt = s.short.trim()
  return `[${s.n}] ${where}${excerpt ? ` – „${excerpt}“` : ''}`
}

export function toMarkdown(inp: ShareInput): string {
  const src = citedSources(inp.answer, inp.sources)
  return [
    `**${inp.labels.question}:** ${inp.question}`,
    inp.answer.trim(),
    src.length ? `### ${inp.labels.sources}\n\n${src.map((s) => `- ${sourceLine(s)}`).join('\n')}` : '',
    `_${inp.labels.footer}_`,
  ].filter(Boolean).join('\n\n') + '\n'
}

export function toPlain(inp: ShareInput): string {
  const src = citedSources(inp.answer, inp.sources)
  return [
    `${inp.labels.question}: ${inp.question}`,
    inp.answer.trim().replace(/\*\*([^*]+)\*\*/g, '$1'),
    src.length ? `${inp.labels.sources}\n${src.map(sourceLine).join('\n')}` : '',
    inp.labels.footer,
  ].filter(Boolean).join('\n\n')
}

const escapeHTML = (s: string) =>
  s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;')

// inline renders **bold** in already escaped text.
const inline = (s: string) => escapeHTML(s).replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>')

const listLine = /^\s*([-*]|\d+\.)\s+/

export function toHTML(inp: ShareInput): string {
  const blocks = inp.answer.trim().split(/\n\s*\n+/).map((p) => {
    const lines = p.split('\n')
    if (lines.every((l) => listLine.test(l))) {
      return `<ul>${lines.map((l) => `<li>${inline(l.replace(listLine, ''))}</li>`).join('')}</ul>`
    }
    return `<p>${lines.map(inline).join('<br>')}</p>`
  })
  const src = citedSources(inp.answer, inp.sources)
  return [
    `<p><strong>${escapeHTML(inp.labels.question)}:</strong> ${escapeHTML(inp.question)}</p>`,
    ...blocks,
    src.length ? `<h3>${escapeHTML(inp.labels.sources)}</h3><p>${src.map((s) => escapeHTML(sourceLine(s))).join('<br>')}</p>` : '',
    `<p><em>${escapeHTML(inp.labels.footer)}</em></p>`,
  ].filter(Boolean).join('\n')
}
