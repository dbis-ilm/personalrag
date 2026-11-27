import { Fragment, KeyboardEvent, ReactNode, useEffect, useRef, useState } from 'react'
import { Events } from '@wailsio/runtime'
import { AppService, Source, Turn } from '../../bindings/github.com/dbis-ilm/personalrag'
import Icon from '../components/Icon'
import { toast } from '../components/Toasts'
import { errorText } from '../App'
import { fmtNumber, note, t } from '../i18n'

type Mode = 'answer' | 'search'

// A turn as shown: the stored Turn plus client-only state while it runs.
type TurnView = Omit<Turn, 'sources'> & { sources: Source[]; searching?: boolean; streaming?: boolean; notice?: string; millis?: number; loadingMore?: boolean; extending?: boolean }

const isMac = document.documentElement.classList.contains('platform-mac')
const mod = () => (isMac ? '⌘' : t('common.ctrl'))
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
        ? { ...t, streaming: false, extending: false, done: true, confidence: d.confidence, note: d.note, error: d.error, hasMore: d.hasMore }
        : t)))
    })
    // Sources added by "evaluate all further matches" arrive before the
    // text that cites them.
    const offSources = Events.On('answer:sources', (ev) => {
      const { turnId, sources } = ev.data
      setTurns((ts) => ts.map((t) => (t.id === turnId ? { ...t, sources: [...t.sources, ...(sources ?? [])] } : t)))
    })
    return () => { offChunk(); offDone(); offSources() }
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
      id: -Date.now(), question: q, standalone: '', mode: m, sources: [], hasMore: false, answer: '', confidence: '', note: '', error: '', done: false, searching: true,
    }
    setTurns((ts) => [...ts, placeholder])
    try {
      const res = await AppService.Search(conversationId, q, scopeId, m)
      const found = res.sources ?? []
      const wantAnswer = m === 'answer' && found.length > 0
      const turn: TurnView = {
        ...placeholder, id: res.turnId, standalone: res.standalone, sources: found, hasMore: res.hasMore,
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

  // showMore appends the next matches of a search turn.
  const showMore = async (index: number) => {
    const turn = turnsRef.current[index]
    if (!turn || turn.loadingMore) return
    const patch = (p: Partial<TurnView>) => setTurns((ts) => ts.map((x) => (x.id === turn.id ? { ...x, ...p } : x)))
    patch({ loadingMore: true })
    try {
      const r = await AppService.MoreSources(turn.id)
      const added = r.sources ?? []
      setTurns((ts) => ts.map((x) => (x.id === turn.id ? { ...x, sources: [...x.sources, ...added], hasMore: r.hasMore, loadingMore: false } : x)))
      if (added.length === 0) toast(t('ask.noMore'))
    } catch (e) {
      patch({ loadingMore: false })
      toast(errorText(e), 'error')
    }
  }

  // askMore evaluates the matches beyond the first page and extends the
  // answer; progress arrives as answer events.
  const askMore = async (turn: TurnView) => {
    if (turn.extending) return
    setTurns((ts) => ts.map((x) => (x.id === turn.id ? { ...x, extending: true, streaming: true } : x)))
    try {
      await AppService.AskMore(turn.id)
    } catch (e) {
      setTurns((ts) => ts.map((x) => (x.id === turn.id ? { ...x, extending: false, streaming: false } : x)))
      toast(errorText(e), 'error')
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
  const MOD = mod()

  return (
    <>
      <main className="ask">
        <div className="ask-bar">
          <span className="scope-chip">
            {scopeId ? t('ask.scopeIn', { name: scopeName }) : t('ask.scopeAll')}
            {scopeId && (
              <button type="button" aria-label={t('ask.clearScope')} onClick={onClearScope} className="chip-x">
                <Icon name="close" size={12} strokeWidth={2.4} />
              </button>
            )}
          </span>
          <div style={{ flex: 1 }} />
          {conversationId && (
            <>
              <button type="button" className="btn" onClick={() => onConversation('', scopeId)} title={`${t('app.newConversation')} (${MOD} N)`}>
                <Icon name="plus" size={14} strokeWidth={2.2} />{t('ask.new')}
              </button>
              <button
                type="button" className={`btn${confirmDelete ? ' btn-danger' : ' btn-icon'}`} onClick={removeConversation} onBlur={() => setConfirmDelete(false)}
                aria-label={t('ask.delete')}
              >
                {confirmDelete ? t('ask.confirmDelete') : <Icon name="trash" size={14} />}
              </button>
            </>
          )}
          <div className="segmented" role="group" aria-label={t('ask.mode')}>
            <button type="button" aria-pressed={mode === 'answer'} disabled={searchOnly} title={searchOnly ? t('ask.noAnswerModel') : undefined} onClick={() => setMode('answer')}>{t('ask.answer')}</button>
            <button type="button" aria-pressed={mode === 'search'} onClick={() => setMode('search')}>{t('ask.searchOnly')}</button>
          </div>
          <button
            type="button" className="btn btn-icon" aria-controls="source-panel" aria-expanded={previewOpen}
            aria-label={previewOpen ? t('ask.hidePanel') : t('ask.showPanel')}
            title={`${previewOpen ? t('ask.hidePanel') : t('ask.showPanel')} (${MOD} J)`}
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
                    <h1 className="question">{t('ask.emptyTitle')}</h1>
                    <p className="lead">{t('ask.emptyLead')} {searchOnly ? t('ask.emptySearchOnly') : t('ask.emptyToggle', { key: MOD })}</p>
                  </>
                ) : (
                  <>
                    <h1 className="question">{t('ask.noDocsTitle')}</h1>
                    <p className="lead">{t('ask.noDocsLead')}</p>
                    <button type="button" className="btn btn-primary" style={{ minHeight: 38 }} onClick={onAddFolder}>
                      <Icon name="plus" size={14} strokeWidth={2.2} />{t('common.addFolder')}
                    </button>
                  </>
                )}
              </div>
            )}

            {turns.map((turn, ti) => (
              <article key={turn.id} className="turn" aria-label={t('ask.turn', { n: ti + 1 })}>
                <h1 className={ti === 0 ? 'question' : 'question followup'}>{turn.question}</h1>
                {turn.standalone && <div className="meta-line">{t('ask.searchedFor', { q: turn.standalone })}</div>}

                <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
                  <div className="meta-line">
                    <Icon name="search" size={13} strokeWidth={2} />
                    {turn.searching
                      ? <span>{t('ask.searching')}</span>
                      : <span>{turn.hasMore ? t('ask.hitsMore', { count: turn.sources.length }) : t('ask.hits', { count: turn.sources.length })}{turn.millis !== undefined && ` · ${fmtNumber(turn.millis / 1000, 2)} s`}</span>}
                  </div>
                  {turn.notice && <div className="inline-note"><Icon name="alert" size={13} strokeWidth={2.2} />{turn.notice}</div>}
                  {!turn.searching && turn.sources.length === 0 && !turn.error && (
                    <p className="lead">{t('ask.noHits')}</p>
                  )}

                  {turn.mode === 'answer' ? (
                    <div className="source-grid">
                      {turn.sources.length > 4 && (
                        <span className="muted source-rest">{t('ask.moreSources', { count: turn.sources.length - 4 })}</span>
                      )}
                      {turn.sources.slice(0, 4).map((s, i) => (
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
                      {turn.sources.map((s, i) => (
                        <button key={s.chunkId} type="button" className="hit-row" aria-pressed={active.turn === ti && active.src === i} onClick={() => show(ti, i)}>
                          <span className="muted" style={{ fontSize: 12, fontWeight: 700, paddingTop: 2 }}>{s.n}</span>
                          <span style={{ display: 'flex', flexDirection: 'column', gap: 4, minWidth: 0 }}>
                            <span style={{ fontWeight: 600 }}>{s.file} <span className="muted" style={{ fontWeight: 400 }}>· {s.location}</span></span>
                            <span style={{ fontSize: 13, lineHeight: 1.5, color: 'var(--text-2)' }}>{s.short}</span>
                          </span>
                          <span className="score-bar" aria-label={t('ask.relevance', { pct: Math.round(s.score * 100) })}><span style={{ width: `${Math.round(s.score * 100)}%` }} /></span>
                        </button>
                      ))}
                      {turn.hasMore && !turn.searching && (
                        <button type="button" className="btn more-btn" disabled={turn.loadingMore} onClick={() => showMore(ti)}>
                          {turn.loadingMore ? t('ask.loadingMore') : t('ask.more')}
                        </button>
                      )}
                    </div>
                  )}
                </div>

                {turn.mode === 'answer' && !turn.searching && turn.sources.length > 0 && (
                  <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
                    <div className="answer" aria-live={turn.streaming ? 'polite' : undefined}>
                      {turn.answer
                        ? renderAnswer(turn.answer, (n) => {
                          const i = turn.sources.findIndex((s) => s.n === n)
                          if (i >= 0) show(ti, i)
                        }, !!turn.streaming)
                        : turn.streaming && (
                          <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
                            <span className="muted" style={{ fontSize: 12 }}>{t('ask.generating')}</span>
                            <div className="skeleton" style={{ width: '92%' }} />
                            <div className="skeleton" style={{ width: '70%' }} />
                          </div>
                        )}
                    </div>
                    {turn.done && !turn.error && turn.confidence && (
                      <div className="answer-foot">
                        <span className={`chip ${turn.confidence === 'high' ? 'kind-ok' : turn.confidence === 'medium' ? 'kind-warn' : 'kind-error'}`} style={{ fontSize: 12.5, padding: '5px 10px', borderRadius: 14 }}>
                          <Icon name={turn.confidence === 'high' ? 'check' : 'alert'} size={13} strokeWidth={2.4} />
                          {t('ask.confidence', { level: confidenceLabel(turn.confidence) })}{turn.note && ` · ${turn.note}`}
                        </span>
                        <div style={{ flex: 1 }} />
                        <button type="button" className="btn" onClick={() => navigator.clipboard?.writeText(turn.answer).then(() => toast(t('ask.copied')))}>{t('common.copy')}</button>
                      </div>
                    )}
                    {(turn.extending || (turn.done && turn.hasMore && !turn.error)) && (
                      <div className="more-row" aria-live="polite">
                        {turn.extending
                          ? <span className="muted" style={{ fontSize: 12.5 }}>{t('ask.extending', { count: turn.sources.length })}</span>
                          : (
                            <button type="button" className="btn" onClick={() => askMore(turn)} title={t('ask.askMoreHint')}>
                              <Icon name="search" size={13} strokeWidth={2.2} />{t('ask.askMore')}
                            </button>
                          )}
                      </div>
                    )}
                  </div>
                )}
                {turn.error && <div className="inline-note error"><Icon name="alert" size={13} strokeWidth={2.2} />{note(turn.error)}</div>}
              </article>
            ))}
            <div ref={endRef} />
          </div>
        </div>

        <form className="composer" onSubmit={(e) => { e.preventDefault(); run(draft) }}>
          <div className="composer-box">
            <label htmlFor="q" className="sr-only">{turns.length ? t('ask.followupLabel') : t('ask.questionLabel')}</label>
            <textarea
              id="q" ref={inputRef} rows={1} value={draft} autoFocus disabled={!hasCollections}
              placeholder={!hasCollections ? t('ask.addFolderFirst') : `${turns.length ? t('ask.askFollowup') : t('ask.ask')} … (${t('ask.sendHint')}${searchOnly ? '' : `, ${MOD} K ${t('ask.searchOnly')}`})`}
              onChange={(e) => setDraft(e.target.value)} onKeyDown={onKeyDown}
            />
            <button type="submit" className="send" aria-label={t('ask.send')} disabled={!draft.trim() || busy}>
              <Icon name="send" strokeWidth={2} />
            </button>
          </div>
        </form>
      </main>

      {previewOpen && <div className="preview-scrim" onClick={() => setPreviewOpen(false)} aria-hidden="true" />}
      {previewOpen && (
        <aside id="source-panel" className="preview" aria-label={t('ask.preview')}>
          <div className="preview-head">
            <span className="section-label" style={{ padding: 0 }}>{t('ask.source')}</span>
            <button type="button" className="icon-btn" aria-label={t('ask.hidePanel')} onClick={() => setPreviewOpen(false)}>
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
                <button type="button" className="btn" onClick={() => fileAction(AppService.OpenDocument, current.fullPath)}><Icon name="external" size={14} />{t('common.open')}</button>
                <button type="button" className="btn" onClick={() => fileAction(AppService.RevealDocument, current.fullPath)}>{isMac ? t('ask.revealFinder') : t('ask.revealExplorer')}</button>
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
                {t('ask.excerptNote')}
              </div>
            </>
          ) : (
            <div className="muted" style={{ margin: 'auto', textAlign: 'center', maxWidth: 260, lineHeight: 1.5 }}>
              {t('ask.previewEmpty')}
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

function confidenceLabel(c: string): string {
  return c === 'high' || c === 'medium' || c === 'low' ? t(`conf.${c}`) : c
}

// Stop words of both UI languages; questions may be asked in either.
const STOP = new Set(['about', 'after', 'also', 'been', 'does', 'from', 'have', 'into', 'many', 'much', 'must', 'should', 'some', 'than', 'that', 'their', 'there', 'these', 'they', 'this', 'what', 'when', 'where', 'which', 'while', 'will', 'with', 'would', 'your', 'aber', 'alle', 'auch', 'dass', 'dein', 'deine', 'denn', 'diese', 'dieser', 'eine', 'einen', 'einer', 'haben', 'habe', 'mein', 'meine', 'meiner', 'meinem', 'nach', 'noch', 'oder', 'sein', 'sind', 'soll', 'über', 'unter', 'wann', 'warum', 'welche', 'welcher', 'wenn', 'werden', 'wieso', 'wird', 'wurde', 'gibt', 'gilt', 'steht', 'lange', 'viel', 'muss', 'müssen', 'erfolgen'])

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
      return <button key={i} type="button" className="cite" onClick={() => cite(n)} aria-label={t('ask.showSource', { n })}>{n}</button>
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
