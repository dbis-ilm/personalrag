import { useCallback, useEffect, useState } from 'react'
import { Events } from '@wailsio/runtime'
import { AppService, AppStatus, Collection, ConversationSummary, IndexProgress } from '../bindings/github.com/dbis-ilm/personalrag'
import Icon from './components/Icon'
import Toasts, { toast } from './components/Toasts'
import AskView from './views/AskView'
import SourcesView from './views/SourcesView'
import AgentsView from './views/AgentsView'
import SetupView from './views/SetupView'
import { Key, fmtDateTime, relDay, setLang, t } from './i18n'

export type View = 'ask' | 'sources' | 'agents' | 'settings'

const NAV: { id: View; label: Key; icon: string }[] = [
  { id: 'ask', label: 'nav.ask', icon: 'chat' },
  { id: 'sources', label: 'nav.sources', icon: 'folder' },
  { id: 'agents', label: 'nav.agents', icon: 'plug' },
  { id: 'settings', label: 'nav.settings', icon: 'gear' },
]

export function errorText(e: unknown): string {
  if (e && typeof e === 'object' && 'message' in e) return String((e as { message: unknown }).message)
  return String(e)
}

export default function App() {
  const [status, setStatus] = useState<AppStatus | null>(null)
  const [view, setView] = useState<View>('ask')
  const [collections, setCollections] = useState<Collection[]>([])
  const [history, setHistory] = useState<ConversationSummary[]>([])
  const [conversationId, setConversationId] = useState('')
  const [scope, setScope] = useState<string>('')
  const [progress, setProgress] = useState<IndexProgress | null>(null)

  const reloadCollections = useCallback(() => {
    AppService.Collections().then((v) => setCollections(v ?? [])).catch(() => setCollections([]))
  }, [])
  const reloadHistory = useCallback(() => {
    AppService.Conversations().then((v) => setHistory(v ?? [])).catch(() => setHistory([]))
  }, [])

  useEffect(() => {
    AppService.Status().then((st) => {
      setLang(st.language)
      setStatus(st)
      if (st.lastCrash) toast(t('app.crashed', { when: fmtDateTime(st.lastCrash) }), 'warn')
    }).catch(console.error)
    AppService.IndexProgress().then(setProgress).catch(console.error)
    reloadCollections()
    reloadHistory()
    const offs = [
      Events.On('status:changed', (ev) => { setLang(ev.data.language); setStatus(ev.data); reloadCollections(); reloadHistory() }),
      Events.On('index:progress', (ev) => setProgress(ev.data)),
      Events.On('sources:changed', () => reloadCollections()),
      Events.On('notice', (ev) => toast(ev.data.text, ev.data.kind)),
    ]
    return () => offs.forEach((off) => off())
  }, [reloadCollections, reloadHistory])

  // Mod+N starts a new conversation.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const mac = document.documentElement.classList.contains('platform-mac')
      if ((mac ? e.metaKey : e.ctrlKey) && e.key.toLowerCase() === 'n') {
        e.preventDefault()
        setConversationId('')
        setView('ask')
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])

  const openConversation = (id: string, collectionId: string) => {
    setConversationId(id)
    setScope(collectionId)
    setView('ask')
  }

  // Drop a scope that no longer exists.
  useEffect(() => {
    if (scope && !collections.some((c) => c.id === scope)) setScope('')
  }, [collections, scope])

  const addFolder = async () => {
    try {
      const c = await AppService.AddFolder()
      if (c.id) {
        toast(t('app.collectionCreated', { name: c.name }), 'ok')
        reloadCollections()
      }
    } catch (e) {
      toast(errorText(e), 'error')
    }
  }

  if (!status) return <div className="app" />
  setLang(status.language)

  if (!status.setupDone) {
    return (
      <div className="app">
        <div className="content setup-shell" key={status.language}><SetupView mode="wizard" status={status} onAddFolder={addFolder} collections={collections} /></div>
        <Toasts />
      </div>
    )
  }

  const scopeName = collections.find((c) => c.id === scope)?.name ?? ''
  const showProgress = progress && (progress.active || progress.paused)
  const pct = progress && progress.total > 0 ? Math.round((progress.done / progress.total) * 100) : 0

  return (
    <div className="app">
      <nav className="sidebar" aria-label={t('nav.main')}>
        <div className="brand">
          <div className="brand-logo"><Icon name="logo" strokeWidth={2} /></div>
          <div style={{ display: 'flex', flexDirection: 'column' }}>
            <span className="brand-name">PersonalRAG</span>
            <span className="brand-sub" title={status.remote ? t('app.remoteTitle', { host: status.remote }) : undefined}>{status.remote ? t('app.remote', { host: status.remote }) : status.searchOnly ? t('app.localSearchOnly') : t('app.local', { model: status.llm })}</span>
          </div>
        </div>

        <div className="nav">
          {NAV.map((n) => (
            <button key={n.id} type="button" className="nav-item" aria-current={view === n.id ? 'page' : undefined} onClick={() => setView(n.id)}>
              <Icon name={n.icon} />
              {t(n.label)}
            </button>
          ))}
        </div>

        <div className="nav">
          <div className="section-label">
            <span>{t('app.collections')}</span>
            <button type="button" className="icon-btn" aria-label={t('app.addCollection')} onClick={addFolder}>
              <Icon name="plus" size={14} strokeWidth={2.2} />
            </button>
          </div>
          {collections.length === 0 && (
            <button type="button" className="nav-item muted" onClick={addFolder}>{t('app.addFolderEllipsis')}</button>
          )}
          {collections.length > 0 && (
            <button type="button" className={`nav-item${scope === '' ? ' is-active' : ''}`} aria-pressed={scope === ''} onClick={() => { setScope(''); setView('ask') }}>
              <span className="swatch" style={{ background: 'var(--muted)', borderRadius: 4 }} />
              <span>{t('common.all')}</span>
            </button>
          )}
          {collections.map((c) => (
            <button
              key={c.id} type="button" className={`nav-item${scope === c.id ? ' is-active' : ''}`} aria-pressed={scope === c.id}
              onClick={() => { setScope(c.id); setView('ask') }} title={(c.folders ?? []).join('\n')}
            >
              <span className="swatch" style={{ background: c.color }} />
              <span style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{c.name}</span>
              {c.sharedWithAgents && <span className="muted" style={{ display: 'inline-flex' }}><Icon name="plug" size={13} label={t('app.shared')} /></span>}
              <span className="nav-count">{c.docCount}</span>
            </button>
          ))}
        </div>

        <div className="nav">
          <div className="section-label">
            <span>{t('app.history')}</span>
            <button type="button" className="icon-btn" aria-label={t('app.newConversation')} title={t('app.newConversation')} onClick={() => openConversation('', scope)}>
              <Icon name="plus" size={14} strokeWidth={2.2} />
            </button>
          </div>
          {history.length === 0 && <span className="muted" style={{ padding: '0 10px', fontSize: 12 }}>{t('app.noConversations')}</span>}
          {history.slice(0, 50).map((h) => (
            <button
              key={h.id} type="button" className={`nav-item history-item${conversationId === h.id ? ' is-active' : ''}`}
              aria-current={conversationId === h.id ? 'true' : undefined} title={h.title}
              onClick={() => openConversation(h.id, h.collectionId)}
            >
              <span className="history-title">{h.title}</span>
              <span className="nav-count">{relDay(h.updatedAt)}</span>
            </button>
          ))}
        </div>

        {showProgress && progress && (
          <div className="progress-card" aria-live="polite">
            <div style={{ display: 'flex', justifyContent: 'space-between', gap: 8 }}>
              <span style={{ fontWeight: 600 }}>{progress.paused ? t('app.indexPaused') : t('app.indexing', { name: progress.collection })}</span>
              <span className="muted" style={{ fontVariantNumeric: 'tabular-nums' }}>{progress.done} / {progress.total}</span>
            </div>
            <div className="progress-track"><div className="progress-bar" style={{ width: `${pct}%` }} /></div>
            <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
              <span className="muted" style={{ flex: 1, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{progress.current || ' '}</span>
              <button type="button" className="btn btn-sm btn-icon" onClick={() => AppService.SetIndexPaused(!progress.paused)} aria-label={progress.paused ? t('app.resume') : t('app.pause')}>
                <Icon name={progress.paused ? 'play' : 'pause'} size={13} strokeWidth={2.2} />
              </button>
            </div>
          </div>
        )}
      </nav>

      <div className="content" key={status.language}>
        {status.error && view !== 'settings' && (
          <div className="banner" role="alert">
            <Icon name="alert" size={15} strokeWidth={2.2} />
            <span style={{ flex: 1 }}>{status.error}</span>
            <button type="button" className="btn btn-sm" onClick={() => setView('settings')}>{t('nav.settings')}</button>
          </div>
        )}
        {view === 'ask' && (
          <AskView
            scopeId={scope} scopeName={scopeName} searchOnly={status.searchOnly}
            hasCollections={collections.length > 0} onAddFolder={addFolder}
            onClearScope={() => setScope('')}
            conversationId={conversationId} onConversation={openConversation} onChanged={reloadHistory}
          />
        )}
        {view === 'sources' && <SourcesView onAddFolder={addFolder} />}
        {view === 'agents' && <AgentsView collections={collections} onChanged={reloadCollections} />}
        {view === 'settings' && (
          <SetupView mode="settings" status={status} onAddFolder={addFolder} collections={collections}
            onHistoryCleared={() => { setConversationId(''); reloadHistory() }} />
        )}
      </div>
      <Toasts />
    </div>
  )
}
