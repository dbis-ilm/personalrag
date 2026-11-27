import { Fragment, KeyboardEvent, ReactNode, useEffect, useRef, useState } from 'react'
import { Events } from '@wailsio/runtime'
import { AppService, Source, Turn } from '../../bindings/github.com/dbis-ilm/personalrag'
import Icon from '../components/Icon'
import { toast } from '../components/Toasts'
import { errorText } from '../App'

type Mode = 'answer' | 'search'

// A turn as shown: the stored Turn plus client-only state while it runs.
type TurnView = Omit<Turn, 'sources'> & { sources: Source[]; searching?: boolean; streaming?: boolean; notice?: string; millis?: number }

const isMac = document.documentElement.classList.contains('platform-mac')
const MOD = isMac ? '⌘' : 'Strg'
const narrow = () => window.matchMedia('(max-width: 1180px)').matches

export default function AskView({
  conversationId, onConversation, onChanged, scopeId, scopeName, searchOnly, hasCollections, onAddFolder, onClearScope,
}: {
  conversationId: string
  onConversation: (id: string, collectionId: string) => void
  onChanged: () => void
  scopeId: string
  scopeName: string
  searchOnly: boolean
  hasCollections: boolean
  onAddFolder: () => void
  onClearScope: () => void
}) {
  const [mode, setMode] = useState<Mode>(searchOnly ? 'search' : 'answer')
  const [draft, setDraft] = useState('')
  const [turns, setTurns] = useState<TurnView[]>([])
  const turnsRef = useRef<TurnView[]>([])
  turnsRef.current = turns
  const [loading, setLoading] = useState(false)
  const [active, setActive] = useState({ turn: 0, src: 0 })
  const [confirmDelete, setConfirmDelete] = useState(false)
  const [previewOpen, setPreviewOpenState] = useState(() => readPref('previewOpen', true) && !narrow())
  const ownId = useRef('') // conversation this view just created; don't reload it
  const endRef = useRef<HTMLDivElement>(null)
  const inputRef = useRef<HTMLTextAreaElement>(null)

  const setPreviewOpen = (open: boolean) => {
    setPreviewOpenState(open)
    writePref('previewOpen', open)
  }
  const show = (turn: number, src: number) => {
    setActive({ turn, src })
    if (!previewOpen) setPreviewOpen(true)
  }

  useEffect(() => { if (searchOnly) setMode('search') }, [searchOnly])

  // Load a stored conversation when the sidebar selects one.
  useEffect(() => {
    setConfirmDelete(false)
    if (!conversationId) {
      ownId.current = ''
      setTurns([])
      inputRef.current?.focus()
      return
    }
    if (conversationId === ownId.current) return
    let cancelled = false
    setLoading(true)
    AppService.Conversation(conversationId)
      .then((d) => {
        if (cancelled) return
        const ts: TurnView[] = (d.turns ?? []).map((t) => ({ ...t, sources: t.sources ?? [] }))
        setTurns(ts)
        const last = Math.max(0, ts.length - 1)
        setActive({ turn: last, src: 0 })
      })
      .catch((e) => toast(errorText(e), 'error'))
      .finally(() => !cancelled && setLoading(false))
    return () => { cancelled = true }
  }, [conversationId])

  // Streamed answers are matched to their turn by ID.
  useEffect(() => {
    const offChunk = Events.On('answer:chunk', (ev) => {
      const { turnId, text } = ev.data
      setTurns((ts) => ts.map((t) => (t.id === turnId ? { ...t, answer: t.answer + text } : t)))
    })
    const offDone = Events.On('answer:done', (ev) => {
      const d = ev.data
      setTurns((ts) => ts.map((t) => (t.id === d.turnId
        ? { ...t, streaming: false, done: true, confidence: d.confidence, note: d.note, error: d.error }
        : t)))
    })
    return () => { offChunk(); offDone() }
  }, [])

  useEffect(() => {
    const onKey = (e: globalThis.KeyboardEvent) => {
      const mod = isMac ? e.metaKey : e.ctrlKey
      if (mod && e.key.toLowerCase() === 'j') {
        e.preventDefault()
        setPreviewOpenState((o) => { writePref('previewOpen', !o); return !o })
        return
      }
      if (e.key === 'Escape' && narrow()) {
        setPreviewOpenState((o) => { if (o) writePref('previewOpen', false); return false })
        return
      }
      if (mod && e.key.toLowerCase() === 'k' && !searchOnly) {
        e.preventDefault()
        setMode((m) => (m === 'answer' ? 'search' : 'answer'))
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [searchOnly])

  // Keep the newest turn in view.
  useEffect(() => { endRef.current?.scrollIntoView({ block: 'end' }) }, [turns.length])

  const busy = turns.some((t) => t.searching || t.streaming)

  const run = async (q: string) => {
    q = q.trim()
    if (!q || busy) return
    const m = mode
    setDraft('')
    const placeholder: TurnView = {
      id: -Date.now(), question: q, standalone: '', mode: m, sources: [], answer: '', confidence: '', note: '', error: '', done: false, searching: true,
    }
    setTurns((ts) => [...ts, placeholder])
    try {
      const res = await AppService.Search(conversationId, q, scopeId, m)
      const found = res.sources ?? []
      const wantAnswer = m === 'answer' && found.length > 0
      const turn: TurnView = {
        ...placeholder, id: res.turnId, standalone: res.standalone, sources: found,
        notice: res.notice, millis: res.millis, searching: false, streaming: wantAnswer, done: !wantAnswer,
      }
      const index = turnsRef.current.findIndex((t) => t.id === placeholder.id)
      setTurns((ts) => ts.map((t) => (t.id === placeholder.id ? turn : t)))
      setActive({ turn: index >= 0 ? index : turnsRef.current.length - 1, src: 0 })
      if (res.conversationId !== conversationId) {
        ownId.current = res.conversationId
        onConversation(res.conversationId, scopeId)
      }
      onChanged()
      if (wantAnswer) await AppService.Ask(res.turnId)
    } catch (e) {
      setTurns((ts) => ts.map((t) => (t.id === placeholder.id || t.searching || t.streaming
        ? { ...t, searching: false, streaming: false, done: true, error: errorText(e) }
        : t)))
    }
  }

  const onKeyDown = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      run(draft)
    }
  }

  const removeConversation = async () => {
    if (!confirmDelete) { setConfirmDelete(true); return }
    setConfirmDelete(false)
    try {
      await AppService.DeleteConversation(conversationId)
      onConversation('', scopeId)
      onChanged()
    } catch (e) {
      toast(errorText(e), 'error')
    }
  }

  const fileAction = (fn: (p: string) => Promise<void>, path: string) => {
    fn(path).catch((e) => toast(errorText(e), 'error'))
  }

  const activeTurn = turns[active.turn]
  const current: Source | undefined = activeTurn?.sources[active.src]
  const terms = queryTerms(activeTurn ? `${activeTurn.question} ${activeTurn.standalone}` : '')

  return (
    <>
      <main className="ask">
        <div className="ask-bar">
          <span className="scope-chip">
            {scopeId ? <>Suche in: {scopeName}</> : <>Suche in: allen Sammlungen</>}
            {scopeId && (
              <button type="button" aria-label="Sammlungsfilter entfernen" onClick={onClearScope} className="chip-x">
                <Icon name="close" size={12} strokeWidth={2.4} />
              </button>
            )}
          </span>
          <div style={{ flex: 1 }} />
          {conversationId && (
            <>
              <button type="button" className="btn" onClick={() => onConversation('', scopeId)} title={`Neue Unterhaltung (${MOD} N)`}>
                <Icon name="plus" size={14} strokeWidth={2.2} />Neu
              </button>
              <button
                type="button" className={`btn${confirmDelete ? ' btn-danger' : ' btn-icon'}`} onClick={removeConversation} onBlur={() => setConfirmDelete(false)}
                aria-label="Unterhaltung löschen"
              >
                {confirmDelete ? 'Wirklich löschen?' : <Icon name="trash" size={14} />}
              </button>
            </>
          )}
          <div className="segmented" role="group" aria-label="Modus">
            <button type="button" aria-pressed={mode === 'answer'} disabled={searchOnly} title={searchOnly ? 'Kein Antwortmodell eingerichtet' : undefined} onClick={() => setMode('answer')}>Antwort</button>
            <button type="button" aria-pressed={mode === 'search'} onClick={() => setMode('search')}>Nur Suche</button>
          </div>
          <button
            type="button" className="btn btn-icon" aria-controls="source-panel" aria-expanded={previewOpen}
            aria-label={previewOpen ? 'Quellenfenster ausblenden' : 'Quellenfenster einblenden'}
            title={`${previewOpen ? 'Quellenfenster ausblenden' : 'Quellenfenster einblenden'} (${MOD} J)`}
            onClick={() => setPreviewOpen(!previewOpen)}
          >
            <Icon name={previewOpen ? 'panelClose' : 'panelOpen'} size={16} />
          </button>
        </div>

        <div className="scroll">
          <div className="thread">
            {turns.length === 0 && !loading && (
              <div className="empty">
                {hasCollections ? (
                  <>
                    <h1 className="question">Was möchtest du in deinen Dokumenten nachsehen?</h1>
                    <p className="lead">Antworten nennen immer ihre Quellen. Unterhaltungen werden lokal gespeichert. {searchOnly ? 'Ohne Antwortmodell siehst du die passenden Fundstellen.' : `Mit ${MOD} K wechselst du in den reinen Suchmodus.`}</p>
                  </>
                ) : (
                  <>
                    <h1 className="question">Noch keine Dokumente</h1>
                    <p className="lead">Füge einen Ordner hinzu. PersonalRAG indexiert ihn lokal und hält ihn aktuell.</p>
                    <button type="button" className="btn btn-primary" style={{ minHeight: 38 }} onClick={onAddFolder}>
                      <Icon name="plus" size={14} strokeWidth={2.2} />Ordner hinzufügen
                    </button>
                  </>
                )}
              </div>
            )}

            {turns.map((t, ti) => (
              <article key={t.id} className="turn" aria-label={`Frage ${ti + 1}`}>
                <h1 className={ti === 0 ? 'question' : 'question followup'}>{t.question}</h1>
                {t.standalone && <div className="meta-line">Gesucht nach: „{t.standalone}“</div>}

                <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
                  <div className="meta-line">
                    <Icon name="search" size={13} strokeWidth={2} />
                    {t.searching
                      ? <span>Suche läuft …</span>
                      : <span>{t.sources.length} Treffer{t.millis !== undefined && ` · ${(t.millis / 1000).toFixed(2).replace('.', ',')} s`}</span>}
                  </div>
                  {t.notice && <div className="inline-note"><Icon name="alert" size={13} strokeWidth={2.2} />{t.notice}</div>}
                  {!t.searching && t.sources.length === 0 && !t.error && (
                    <p className="lead">Keine passenden Stellen gefunden. Versuche andere Begriffe oder eine andere Sammlung.</p>
                  )}

                  {t.mode === 'answer' ? (
                    <div className="source-grid">
                      {t.sources.slice(0, 4).map((s, i) => (
                        <button key={s.chunkId} type="button" className="source-card" aria-pressed={active.turn === ti && active.src === i} onClick={() => show(ti, i)}>
                          <span style={{ display: 'flex', alignItems: 'center', gap: 8, minWidth: 0 }}>
                            <span className="source-num">{s.n}</span>
                            <span className="source-file">{s.file}</span>
                          </span>
                          <span className="source-loc">{s.location}</span>
                          <span className="source-short">„{s.short}“</span>
                        </button>
                      ))}
                    </div>
                  ) : (
                    <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
                      {t.sources.map((s, i) => (
                        <button key={s.chunkId} type="button" className="hit-row" aria-pressed={active.turn === ti && active.src === i} onClick={() => show(ti, i)}>
                          <span className="muted" style={{ fontSize: 12, fontWeight: 700, paddingTop: 2 }}>{s.n}</span>
                          <span style={{ display: 'flex', flexDirection: 'column', gap: 4, minWidth: 0 }}>
                            <span style={{ fontWeight: 600 }}>{s.file} <span className="muted" style={{ fontWeight: 400 }}>· {s.location}</span></span>
                            <span style={{ fontSize: 13, lineHeight: 1.5, color: 'var(--text-2)' }}>{s.short}</span>
                          </span>
                          <span className="score-bar" aria-label={`Relevanz ${Math.round(s.score * 100)} %`}><span style={{ width: `${Math.round(s.score * 100)}%` }} /></span>
                        </button>
                      ))}
                    </div>
                  )}
                </div>

                {t.mode === 'answer' && !t.searching && t.sources.length > 0 && (
                  <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
                    <div className="answer" aria-live={t.streaming ? 'polite' : undefined}>
                      {t.answer
                        ? renderAnswer(t.answer, (n) => {
                          const i = t.sources.findIndex((s) => s.n === n)
                          if (i >= 0) show(ti, i)
                        }, !!t.streaming)
                        : t.streaming && (
                          <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
                            <span className="muted" style={{ fontSize: 12 }}>Quellen werden bewertet, Antwort wird erzeugt …</span>
                            <div className="skeleton" style={{ width: '92%' }} />
                            <div className="skeleton" style={{ width: '70%' }} />
                          </div>
                        )}
                    </div>
                    {t.done && !t.error && t.confidence && (
                      <div className="answer-foot">
                        <span className={`chip ${t.confidence === 'hoch' ? 'kind-ok' : t.confidence === 'mittel' ? 'kind-warn' : 'kind-error'}`} style={{ fontSize: 12.5, padding: '5px 10px', borderRadius: 14 }}>
                          <Icon name={t.confidence === 'hoch' ? 'check' : 'alert'} size={13} strokeWidth={2.4} />
                          Konfidenz {t.confidence}{t.note && ` · ${t.note}`}
                        </span>
                        <div style={{ flex: 1 }} />
                        <button type="button" className="btn" onClick={() => navigator.clipboard?.writeText(t.answer).then(() => toast('Antwort kopiert'))}>Kopieren</button>
                      </div>
                    )}
                  </div>
                )}
                {t.error && <div className="inline-note error"><Icon name="alert" size={13} strokeWidth={2.2} />{t.error}</div>}
              </article>
            ))}
            <div ref={endRef} />
          </div>
        </div>

        <form className="composer" onSubmit={(e) => { e.preventDefault(); run(draft) }}>
          <div className="composer-box">
            <label htmlFor="q" className="sr-only">{turns.length ? 'Folgefrage' : 'Frage'}</label>
            <textarea
              id="q" ref={inputRef} rows={1} value={draft} autoFocus disabled={!hasCollections}
              placeholder={!hasCollections ? 'Zuerst einen Ordner hinzufügen' : `${turns.length ? 'Folgefrage stellen' : 'Frage stellen'} … (↵ senden${searchOnly ? '' : `, ${MOD} K Nur Suche`})`}
              onChange={(e) => setDraft(e.target.value)} onKeyDown={onKeyDown}
            />
            <button type="submit" className="send" aria-label="Frage senden" disabled={!draft.trim() || busy}>
              <Icon name="send" strokeWidth={2} />
            </button>
          </div>
        </form>
      </main>

      {previewOpen && <div className="preview-scrim" onClick={() => setPreviewOpen(false)} aria-hidden="true" />}
      {previewOpen && (
        <aside id="source-panel" className="preview" aria-label="Quellenvorschau">
          <div className="preview-head">
            <span className="section-label" style={{ padding: 0 }}>Quelle</span>
            <button type="button" className="icon-btn" aria-label="Quellenfenster ausblenden" onClick={() => setPreviewOpen(false)}>
              <Icon name="close" size={14} strokeWidth={2.2} />
            </button>
          </div>
          {current ? (
            <>
              <div style={{ display: 'flex', alignItems: 'flex-start', gap: 10 }}>
                <span className="source-num" style={{ minWidth: 24, height: 24, fontSize: 12 }}>{current.n}</span>
                <div style={{ display: 'flex', flexDirection: 'column', gap: 3, minWidth: 0 }}>
                  <span style={{ fontSize: 15, fontWeight: 650, overflowWrap: 'anywhere' }}>{current.file}</span>
                  <span className="muted" style={{ fontSize: 12, overflowWrap: 'anywhere' }}>{current.dir}</span>
                </div>
              </div>
              <div style={{ display: 'flex', flexWrap: 'wrap', gap: 8 }}>
                <button type="button" className="btn" onClick={() => fileAction(AppService.OpenDocument, current.fullPath)}><Icon name="external" size={14} />Öffnen</button>
                <button type="button" className="btn" onClick={() => fileAction(AppService.RevealDocument, current.fullPath)}>{isMac ? 'Im Finder zeigen' : 'Im Explorer zeigen'}</button>
              </div>
              <div className="excerpt">
                <div className="excerpt-meta">{current.location}</div>
                {current.before && <p className="excerpt-context">{tidy(current.before)}</p>}
                <div className="excerpt-hit">
                  {paragraphs(current.highlight).map((p, i) => <p key={i}>{emphasize(p, terms)}</p>)}
                </div>
                {current.after && <p className="excerpt-context">{tidy(current.after)}</p>}
              </div>
              <div className="muted" style={{ display: 'flex', alignItems: 'center', gap: 8, fontSize: 12 }}>
                <Icon name="lock" size={13} strokeWidth={2} />
                Textauszug zum Zeitpunkt der Frage · Datei wird nie verändert
              </div>
            </>
          ) : (
            <div className="muted" style={{ margin: 'auto', textAlign: 'center', maxWidth: 260, lineHeight: 1.5 }}>
              Hier erscheint die Fundstelle, sobald du eine Quelle oder Zitatmarke anklickst.
            </div>
          )}
        </aside>
      )}
    </>
  )
}

// tidy turns extracted text (hard PDF line breaks, hyphenation) into flowing
// text: hyphens at line ends are joined, single line breaks become spaces.
function tidy(s: string): string {
  return s
    .replace(/(\p{L})-\n[ \t]*(\p{Ll})/gu, '$1$2') // "Kündi-\ngung" → "Kündigung"
    .replace(/(\p{L})-\n[ \t]*(?=\p{Lu})/gu, '$1-') // "BGB-\nVorschrift" keeps its hyphen
    .split(/\n[ \t]*\n\s*/)
    .map((p) => p.replace(/\s+/g, ' ').trim())
    .filter((p) => p !== '')
    .join('\n\n')
}

function paragraphs(s: string): string[] {
  return tidy(s).split(/\n\n/).filter((p) => p.trim() !== '')
}

const STOP = new Set(['aber', 'alle', 'auch', 'dass', 'dein', 'deine', 'denn', 'diese', 'dieser', 'eine', 'einen', 'einer', 'haben', 'habe', 'mein', 'meine', 'meiner', 'meinem', 'nach', 'noch', 'oder', 'sein', 'sind', 'soll', 'über', 'unter', 'wann', 'warum', 'welche', 'welcher', 'wenn', 'werden', 'wieso', 'wird', 'wurde', 'gibt', 'gilt', 'steht', 'lange', 'viel', 'muss', 'müssen', 'erfolgen'])

// queryTerms are the question's content words (four letters or more).
function queryTerms(q: string): string[] {
  const words = q.toLowerCase().match(/[\p{L}\p{N}]{4,}/gu) ?? []
  return [...new Set(words.filter((w) => !STOP.has(w)))]
}

// emphasize marks words starting with a query term ("kündigung" → "Kündigungsfrist").
function emphasize(text: string, terms: string[]): ReactNode {
  if (terms.length === 0) return text
  const esc = terms.map((t) => t.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'))
  const re = new RegExp(`(?<![\\p{L}\\p{N}])((?:${esc.join('|')})[\\p{L}\\p{N}]*)`, 'giu')
  return text.split(re).map((part, i) => (i % 2 === 1 ? <mark key={i} className="term">{part}</mark> : <Fragment key={i}>{part}</Fragment>))
}

// Renders paragraphs with **bold**, "- " list lines and [n] citation markers.
function renderAnswer(text: string, cite: (n: number) => void, streaming: boolean): ReactNode {
  const paras = text.split(/\n\s*\n+/)
  const inline = (s: string) => s.split(/(\*\*[^*]+\*\*|\[\d+\])/g).map((part, i) => {
    const c = part.match(/^\[(\d+)\]$/)
    if (c) {
      const n = Number(c[1])
      return <button key={i} type="button" className="cite" onClick={() => cite(n)} aria-label={`Quelle ${n} anzeigen`}>{n}</button>
    }
    if (part.startsWith('**') && part.endsWith('**') && part.length > 4) return <strong key={i}>{part.slice(2, -2)}</strong>
    return <Fragment key={i}>{part}</Fragment>
  })
  return paras.map((p, pi) => {
    const caret = streaming && pi === paras.length - 1 ? <span className="caret" aria-hidden="true" /> : null
    const lines = p.split('\n')
    if (lines.every((l) => /^\s*([-*]|\d+\.)\s+/.test(l))) {
      return (
        <ul key={pi}>
          {lines.map((l, li) => <li key={li}>{inline(l.replace(/^\s*([-*]|\d+\.)\s+/, ''))}{li === lines.length - 1 && caret}</li>)}
        </ul>
      )
    }
    return <p key={pi}>{inline(p)}{caret}</p>
  })
}

// Per-viewer UI preferences; storage may be unavailable, so never rely on it.
function readPref(key: string, fallback: boolean): boolean {
  try {
    const v = localStorage.getItem(`personalrag.${key}`)
    return v === null ? fallback : v === '1'
  } catch {
    return fallback
  }
}

function writePref(key: string, value: boolean) {
  try {
    localStorage.setItem(`personalrag.${key}`, value ? '1' : '0')
  } catch {
    // ignore
  }
}
