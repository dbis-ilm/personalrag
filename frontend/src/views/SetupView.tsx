import { useCallback, useEffect, useState } from 'react'
import { AppService, AppStatus, Collection, ModelInfo, ProviderInfo, RuntimeInfo } from '../../bindings/github.com/dbis-ilm/personalrag'
import Icon from '../components/Icon'
import { toast } from '../components/Toasts'
import { errorText } from '../App'
import { Key, fmtDateTime, t } from '../i18n'

const STEPS: Key[] = ['setup.step1', 'setup.step2', 'setup.step3']

// Setup wizard on first start; the same building blocks as the settings page.
export default function SetupView({ mode, status, collections, onAddFolder, onHistoryCleared, onAbout }: {
  mode: 'wizard' | 'settings'
  status: AppStatus
  collections: Collection[]
  onAddFolder: () => void
  onHistoryCleared?: () => void
  onAbout?: () => void
}) {
  const [confirmClear, setConfirmClear] = useState(false)
  const [step, setStep] = useState(0)
  const [rt, setRt] = useState<RuntimeInfo | null>(null)
  const [checking, setChecking] = useState(false)
  const [llm, setLlm] = useState('')
  const [emb, setEmb] = useState('')
  const [consent, setConsent] = useState(false)
  const [saving, setSaving] = useState(false)

  const check = useCallback(async () => {
    setChecking(true)
    try {
      const info = await AppService.RuntimeInfo()
      setRt(info)
      setEmb((e) => e || info.embedding || info.suggestedEmbedding)
      setLlm((l) => l || (status.setupDone ? info.llm : info.llm || info.suggestedLlm))
    } finally {
      setChecking(false)
    }
  }, [status.setupDone])

  // Re-check whenever the core restarted with other connections or models.
  const statusKey = `${status.ready}|${status.error}|${status.llm}|${status.embedding}|${status.remote}`
  useEffect(() => { check() }, [check, statusKey])

  const providers = rt?.providers ?? []
  const ollama = providers.find((p) => p.id === 'ollama')
  const gpu = providers.find((p) => p.id === 'gpustack')
  const anyOK = providers.some((p) => p.ok)
  const embChanged = !!rt?.embedding && emb !== rt.embedding
  const modelsDirty = !!rt && (emb !== rt.embedding || llm !== rt.llm)
  const usesRemote = !!gpu?.remote && [llm, emb].some((spec) => spec.startsWith('gpustack/'))
  const needsConsent = usesRemote && rt?.remoteConsent !== gpu?.host

  useEffect(() => { setConsent(false) }, [llm, emb])

  const saveModels = async (): Promise<boolean> => {
    setSaving(true)
    try {
      await AppService.SaveModels(llm, emb, consent)
      toast(embChanged ? t('setup.savedRebuild') : t('setup.saved'), 'ok')
      setRt((r) => (r ? { ...r, llm, embedding: emb, remoteConsent: usesRemote && gpu ? gpu.host : r.remoteConsent } : r))
      return true
    } catch (e) {
      toast(errorText(e), 'error')
      return false
    } finally {
      setSaving(false)
    }
  }

  const finish = () => AppService.FinishSetup().catch((e) => toast(errorText(e), 'error'))

  const connectionBlock = <Connections ollama={ollama} gpu={gpu} checking={checking} onRecheck={check} compact={mode === 'wizard'} />

  const modelBlock = (
    <>
      <div className="field">
        <label htmlFor="emb">{t('setup.embLabel')}</label>
        <select id="emb" className="input select" value={emb} onChange={(e) => setEmb(e.target.value)} disabled={!anyOK}>
          {!providers.some((p) => p.models?.some((m) => m.embedding)) && !emb && <option value="">{t('setup.noEmb')}</option>}
          {providers.filter((p) => p.ok).map((p) => (
            <ModelOptions key={p.id} provider={p} models={(p.models ?? []).filter((m) => m.embedding || p.id === 'gpustack')} />
          ))}
        </select>
        {ollama?.ok && !ollama.models?.some((m) => m.embedding) && (
          <span className="muted" style={{ fontSize: 12 }}>{t('setup.installEmb')} <code className="inline-code">ollama pull bge-m3</code> {t('setup.installEmbAfter')}</span>
        )}
        {gpu?.ok && <span className="muted" style={{ fontSize: 12 }}>{t('setup.gpuEmbHint')}</span>}
        {embChanged && <span className="text-warn" style={{ fontSize: 12, fontWeight: 600 }}>{t('setup.embChanged')}</span>}
      </div>
      <div className="field">
        <label htmlFor="llm">{t('setup.llmLabel')}</label>
        <select id="llm" className="input select" value={llm} onChange={(e) => setLlm(e.target.value)} disabled={!anyOK}>
          <option value="">{t('setup.noLlm')}</option>
          {providers.filter((p) => p.ok).map((p) => (
            <ModelOptions key={p.id} provider={p} models={(p.models ?? []).filter((m) => !m.embedding)} />
          ))}
        </select>
      </div>
      {usesRemote && gpu && (needsConsent ? (
        <div className="consent">
          <Icon name="alert" size={16} strokeWidth={2.2} />
          <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
            <strong>{t('setup.consentTitle')}</strong>
            <span>{t('setup.consentBefore')} <code className="inline-code">{gpu.host}</code>{t('setup.consentAfter')}</span>
            <label className="check">
              <input type="checkbox" checked={consent} onChange={(e) => setConsent(e.target.checked)} />
              {t('setup.consentCheck', { host: gpu.host })}
            </label>
          </div>
        </div>
      ) : (
        <span className="muted" style={{ fontSize: 12 }}>{t('setup.consented', { host: gpu.host })}</span>
      ))}
    </>
  )

  const folderBlock = (
    <>
      {collections.length > 0 && (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
          {collections.map((c) => (
            <div key={c.id} className="setup-row">
              <span className="swatch" style={{ background: c.color }} />
              <div style={{ flex: 1, display: 'flex', flexDirection: 'column', gap: 2, minWidth: 0 }}>
                <span style={{ fontWeight: 600 }}>{c.name}</span>
                <span className="muted" style={{ fontSize: 12, overflowWrap: 'anywhere' }}>{(c.folders ?? []).join(', ')}</span>
              </div>
              <span className="muted" style={{ fontSize: 12 }}>{t('src.files', { count: c.docCount })}</span>
            </div>
          ))}
        </div>
      )}
      <button type="button" className="dropzone" data-file-drop-target onClick={onAddFolder} style={{ padding: 22 }}>
        <Icon name="folder" size={24} strokeWidth={1.6} />
        <span style={{ fontWeight: 600, color: 'var(--text)' }}>{collections.length ? t('setup.addAnother') : t('setup.chooseFolder')}</span>
        <span style={{ fontSize: 12 }}>{t('setup.formats')}</span>
      </button>
    </>
  )

  if (mode === 'settings') {
    return (
      <div className="scroll">
        <div className="page" style={{ maxWidth: 760 }}>
          <div className="page-head"><div className="grow"><h1>{t('nav.settings')}</h1><p className="lead">{status.remote ? t('settings.remote', { host: status.remote }) : t('settings.local')} {t('settings.data', { dir: status.dataDir })}</p></div></div>
          {status.error && <div className="inline-note error"><Icon name="alert" size={13} strokeWidth={2.2} />{status.error}</div>}
          <section className="card">
            <h2>{t('settings.language')}</h2>
            <LanguageSelect setting={status.languageSetting} />
            <span className="muted" style={{ fontSize: 12 }}>{t('settings.langHint')}</span>
          </section>
          <section className="card"><h2>{t('settings.connections')}</h2>{connectionBlock}</section>
          <section className="card">
            <h2>{t('settings.models')}</h2>
            {modelBlock}
            <div style={{ display: 'flex', justifyContent: 'flex-end' }}>
              <button type="button" className="btn btn-primary" disabled={!modelsDirty && !needsConsent || !emb || saving || (needsConsent && !consent)} onClick={saveModels}>{saving ? t('common.saving') : t('common.save')}</button>
            </div>
          </section>
          <ExclusionsCard />
          <section className="card"><h2>{t('settings.folders')}</h2>{folderBlock}</section>
          <section className="card">
            <h2>{t('app.history')}</h2>
            <p className="muted" style={{ margin: 0, fontSize: 13 }}>{t('settings.historyLead')}</p>
            <div>
              <button
                type="button" className={`btn${confirmClear ? ' btn-danger' : ''}`} onBlur={() => setConfirmClear(false)}
                onClick={async () => {
                  if (!confirmClear) { setConfirmClear(true); return }
                  setConfirmClear(false)
                  try {
                    await AppService.DeleteAllConversations()
                    toast(t('settings.historyCleared'))
                    onHistoryCleared?.()
                  } catch (e) {
                    toast(errorText(e), 'error')
                  }
                }}
              >
                {confirmClear ? t('settings.confirmClear') : t('settings.clear')}
              </button>
            </div>
          </section>
          <DiagnosticsCard lastCrash={status.lastCrash} />
          {onAbout && <div><button type="button" className="btn" onClick={onAbout}>{t('about.open')}</button></div>}
        </div>
      </div>
    )
  }

  const canNext = step === 0 ? anyOK : step === 1 ? !!emb && (!needsConsent || consent) : collections.length > 0
  const next = async () => {
    if (step === 1 && (modelsDirty || needsConsent || !status.embedding) && !(await saveModels())) return
    setStep(step + 1)
  }

  return (
    <div className="scroll">
      <div className="page" style={{ alignItems: 'center', maxWidth: 'none', paddingTop: 48 }}>
        <div className="brand" style={{ padding: 0 }}>
          <div className="brand-logo"><Icon name="logo" strokeWidth={2} /></div>
          <span className="brand-name" style={{ fontSize: 17 }}>{t('setup.title')}</span>
          <div style={{ marginLeft: 16 }}><LanguageSelect setting={status.languageSetting} compact /></div>
        </div>
        <ol className="steps" aria-label={t('setup.steps')}>
          {STEPS.map((label, i) => (
            <li key={label} className={`step${i === step ? ' is-current' : ''}${i < step ? ' is-done' : ''}`} aria-current={i === step ? 'step' : undefined}>
              <span className="step-dot">{i < step ? <Icon name="check" size={13} strokeWidth={2.6} /> : i + 1}</span>
              {t(label)}
            </li>
          ))}
        </ol>

        <div className="card" style={{ width: '100%', maxWidth: 680, padding: 32, gap: 18 }}>
          {step === 0 && (
            <>
              <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
                <h1 style={{ fontSize: 22 }}>{t('setup.step1')}</h1>
                <p className="lead" style={{ lineHeight: 1.55 }}>{t('setup.s1Lead')}</p>
              </div>
              {connectionBlock}
            </>
          )}
          {step === 1 && (
            <>
              <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
                <h1 style={{ fontSize: 22 }}>{t('setup.step2')}</h1>
                <p className="lead" style={{ lineHeight: 1.55 }}>{t('setup.s2Lead')}</p>
              </div>
              {modelBlock}
            </>
          )}
          {step === 2 && (
            <>
              <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
                <h1 style={{ fontSize: 22 }}>{t('setup.s3Title')}</h1>
                <p className="lead" style={{ lineHeight: 1.55 }}>{t('setup.s3Lead')}</p>
              </div>
              {folderBlock}
            </>
          )}

          <div style={{ display: 'flex', alignItems: 'center', gap: 10, paddingTop: 8, borderTop: '1px solid var(--line-soft)' }}>
            {step > 0 && <button type="button" className="btn" style={{ minHeight: 40, padding: '0 16px' }} onClick={() => setStep(step - 1)}>{t('setup.back')}</button>}
            <div style={{ flex: 1 }} />
            {step < STEPS.length - 1
              ? <button type="button" className="btn btn-primary" style={{ minHeight: 40, padding: '0 18px' }} disabled={!canNext || saving} onClick={next}>{saving ? t('common.saving') : t('setup.next')}</button>
              : <button type="button" className="btn btn-primary" style={{ minHeight: 40, padding: '0 18px' }} disabled={!canNext} onClick={finish}>{t('setup.done')}</button>}
          </div>
        </div>
      </div>
    </div>
  )
}

function ModelOptions({ provider, models }: { provider: ProviderInfo; models: ModelInfo[] }) {
  if (models.length === 0) return null
  const where = provider.remote ? t('model.remote', { host: provider.host }) : t('model.local')
  return (
    <optgroup label={`${provider.label} (${where})`}>
      {models.map((m) => <option key={m.spec} value={m.spec}>{m.name}{m.size ? ` · ${m.size}` : ''}</option>)}
    </optgroup>
  )
}

// Connections: Ollama address and an optional GPUStack server (URL + API
// key in the system keychain), each testable before saving.
function Connections({ ollama, gpu, checking, onRecheck, compact }: {
  ollama?: ProviderInfo
  gpu?: ProviderInfo
  checking: boolean
  onRecheck: () => void
  compact: boolean
}) {
  const [ollamaHost, setOllamaHost] = useState('')
  const [gpuUrl, setGpuUrl] = useState('')
  const [gpuKey, setGpuKey] = useState('')
  const [keySet, setKeySet] = useState(false)
  const [removeKey, setRemoveKey] = useState(false)
  const [showGpu, setShowGpu] = useState(!compact)
  const [tests, setTests] = useState<Record<string, ProviderInfo | undefined>>({})
  const [saving, setSaving] = useState(false)
  const [loaded, setLoaded] = useState({ ollamaHost: '', gpuUrl: '' })

  useEffect(() => {
    AppService.Connections().then((c) => {
      setOllamaHost(c.ollamaHost)
      setGpuUrl(c.gpustackUrl)
      setKeySet(c.gpustackKeySet)
      setLoaded({ ollamaHost: c.ollamaHost, gpuUrl: c.gpustackUrl })
      if (c.gpustackUrl) setShowGpu(true)
    }).catch(console.error)
  }, [])

  const dirty = ollamaHost !== loaded.ollamaHost || gpuUrl !== loaded.gpuUrl || gpuKey !== '' || removeKey

  const test = async (id: 'ollama' | 'gpustack') => {
    setTests((t) => ({ ...t, [id]: undefined }))
    const r = await AppService.TestConnection(id, id === 'ollama' ? ollamaHost : gpuUrl, id === 'gpustack' && !removeKey ? gpuKey : '')
    setTests((t) => ({ ...t, [id]: r }))
  }

  const save = async () => {
    setSaving(true)
    try {
      await AppService.SaveConnections(ollamaHost, gpuUrl, gpuKey, removeKey)
      toast(t('conn.saved'), 'ok')
      setLoaded({ ollamaHost, gpuUrl })
      setKeySet(gpuUrl !== '' && !removeKey && (keySet || gpuKey !== ''))
      setGpuKey('')
      setRemoveKey(false)
      setTimeout(onRecheck, 600)
    } catch (e) {
      toast(errorText(e), 'error')
    } finally {
      setSaving(false)
    }
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
      <div className="conn">
        <ProviderStatus title="Ollama" info={tests.ollama ?? ollama} checking={checking} />
        <div className="conn-row">
          <label htmlFor="ollama-host" className="sr-only">{t('conn.ollamaAddress')}</label>
          <input id="ollama-host" className="input" value={ollamaHost} onChange={(e) => setOllamaHost(e.target.value)} placeholder="http://localhost:11434" spellCheck={false} />
          <button type="button" className="btn" onClick={() => test('ollama')}>{t('conn.test')}</button>
        </div>
      </div>

      {!showGpu ? (
        <button type="button" className="btn" style={{ alignSelf: 'flex-start' }} onClick={() => setShowGpu(true)}>
          <Icon name="plus" size={14} strokeWidth={2.2} />{t('conn.addGpu')}
        </button>
      ) : (
        <div className="conn">
          <ProviderStatus title="GPUStack" info={tests.gpustack ?? (gpuUrl ? gpu : undefined)} checking={checking} emptyText={t('conn.notSetUp')} />
          <div className="field">
            <label htmlFor="gpu-url">{t('conn.apiUrl')}</label>
            <input id="gpu-url" className="input" value={gpuUrl} onChange={(e) => setGpuUrl(e.target.value)} placeholder="https://gpustack.example/v1" spellCheck={false} />
            <span className="muted" style={{ fontSize: 12 }}>{t('conn.apiUrlHint')}</span>
          </div>
          <div className="field">
            <label htmlFor="gpu-key">{t('conn.apiKey')}</label>
            <input id="gpu-key" className="input" type="password" autoComplete="off" value={gpuKey} disabled={removeKey}
              onChange={(e) => setGpuKey(e.target.value)} placeholder={keySet ? t('conn.keySaved') : t('conn.optional')} />
            <span className="muted" style={{ fontSize: 12 }}>{t('conn.keyHint')}</span>
            {keySet && (
              <label className="check">
                <input type="checkbox" checked={removeKey} onChange={(e) => setRemoveKey(e.target.checked)} />
                {t('conn.removeKey')}
              </label>
            )}
          </div>
          <div className="conn-row" style={{ justifyContent: 'flex-start' }}>
            <button type="button" className="btn" onClick={() => test('gpustack')} disabled={!gpuUrl}>{t('conn.test')}</button>
          </div>
        </div>
      )}

      <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
        <button type="button" className="btn" onClick={onRecheck} disabled={checking}>{checking ? t('conn.checkingShort') : t('conn.recheck')}</button>
        <div style={{ flex: 1 }} />
        <button type="button" className="btn btn-primary" onClick={save} disabled={!dirty || saving}>{saving ? t('common.saving') : t('conn.save')}</button>
      </div>
    </div>
  )
}

function ProviderStatus({ title, info, checking, emptyText }: { title: string; info?: ProviderInfo; checking: boolean; emptyText?: string }) {
  let dot = '#bfc2c7'
  let text = checking ? t('conn.checking') : emptyText ?? t('conn.checking')
  if (info && info.configured) {
    if (info.ok) {
      dot = 'var(--ok-dot)'
      text = t('conn.reachable', { url: info.url, count: info.models?.length ?? 0 }) + (info.remote ? ` · ${t('conn.otherMachine')}` : '')
    } else {
      dot = 'var(--err)'
      text = info.error
    }
  }
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
      <span style={{ width: 10, height: 10, borderRadius: 5, background: dot, flex: 'none' }} />
      <span style={{ fontWeight: 600 }}>{title}</span>
      <span className="muted" style={{ fontSize: 12, overflowWrap: 'anywhere' }}>{text}</span>
    </div>
  )
}

// ExclusionsCard: name patterns skipped when indexing, one per line.
function ExclusionsCard() {
  const [text, setText] = useState('')
  const [saved, setSaved] = useState('')
  const [builtin, setBuiltin] = useState<string[]>([])
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    AppService.Exclusions().then((ex) => {
      const joined = (ex.patterns ?? []).join('\n')
      setText(joined)
      setSaved(joined)
      setBuiltin(ex.builtin ?? [])
    }).catch(console.error)
  }, [])

  const save = async () => {
    setSaving(true)
    try {
      await AppService.SaveExclusions(text.split('\n'))
      const ex = await AppService.Exclusions()
      const joined = (ex.patterns ?? []).join('\n')
      setText(joined)
      setSaved(joined)
      toast(t('excl.saved'), 'ok')
    } catch (e) {
      toast(errorText(e), 'error')
    } finally {
      setSaving(false)
    }
  }

  return (
    <section className="card">
      <h2>{t('excl.title')}</h2>
      <div className="field">
        <label htmlFor="excl">{t('excl.label')}</label>
        <textarea id="excl" className="input" rows={4} value={text} onChange={(e) => setText(e.target.value)} placeholder={t('excl.placeholder')} spellCheck={false}
          style={{ padding: '8px 12px', fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Consolas, monospace', fontSize: 12.5, resize: 'vertical' }} />
        <span className="muted" style={{ fontSize: 12 }}>{t('excl.hint')}</span>
        <span className="muted" style={{ fontSize: 12 }}>{t('excl.always', { list: builtin.join(' · ') })}</span>
      </div>
      <div style={{ display: 'flex', justifyContent: 'flex-end' }}>
        <button type="button" className="btn btn-primary" onClick={save} disabled={saving || text === saved}>{saving ? t('common.saving') : t('common.save')}</button>
      </div>
    </section>
  )
}

const LEVELS: { id: string; label: Key }[] = [
  { id: 'error', label: 'log.error' },
  { id: 'warn', label: 'log.warn' },
  { id: 'info', label: 'log.info' },
  { id: 'debug', label: 'log.debug' },
]

// LanguageSelect: follow the system or pin German or English. Language
// names stay in their own language so they can be found either way.
function LanguageSelect({ setting, compact }: { setting: string; compact?: boolean }) {
  const change = (v: string) => AppService.SetLanguage(v).catch((e) => toast(errorText(e), 'error'))
  return (
    <select
      className="input select" value={setting} onChange={(e) => change(e.target.value)} aria-label={t('settings.language')}
      style={compact ? { width: 'auto', minHeight: 30 } : undefined}
    >
      <option value="">{t('settings.langSystem')}</option>
      <option value="de">Deutsch</option>
      <option value="en">English</option>
    </select>
  )
}

// DiagnosticsCard: log level, the log folder and a report to copy into a bug
// report. The report is shown before it is copied, so nothing leaves
// unseen.
function DiagnosticsCard({ lastCrash }: { lastCrash: string }) {
  const [level, setLevel] = useState('info')
  const [report, setReport] = useState('')
  const [loading, setLoading] = useState(false)

  useEffect(() => { AppService.LogLevel().then(setLevel).catch(console.error) }, [])

  const changeLevel = async (v: string) => {
    try {
      await AppService.SetLogLevel(v)
      setLevel(v)
      toast(v === 'debug' ? t('diag.debugOn') : t('diag.levelSaved'), v === 'debug' ? 'warn' : 'ok')
    } catch (e) {
      toast(errorText(e), 'error')
    }
  }

  const show = async () => {
    setLoading(true)
    try {
      setReport(await AppService.Diagnostics())
    } catch (e) {
      toast(errorText(e), 'error')
    } finally {
      setLoading(false)
    }
  }

  return (
    <section className="card">
      <h2>{t('diag.title')}</h2>
      {lastCrash && (
        <div className="inline-note error"><Icon name="alert" size={13} strokeWidth={2.2} />{t('diag.crashed', { when: fmtDateTime(lastCrash) })}</div>
      )}
      <div className="field">
        <label htmlFor="loglevel">{t('diag.log')}</label>
        <select id="loglevel" className="input select" value={level} onChange={(e) => changeLevel(e.target.value)}>
          {LEVELS.map((l) => <option key={l.id} value={l.id}>{t(l.label)}</option>)}
        </select>
        <span className="muted" style={{ fontSize: 12 }}>{t('diag.logHint')}</span>
      </div>
      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 8 }}>
        <button type="button" className="btn" onClick={show} disabled={loading}>{loading ? t('diag.creating') : report ? t('diag.refresh') : t('diag.show')}</button>
        <button type="button" className="btn" onClick={() => AppService.RevealLogs().catch((e) => toast(errorText(e), 'error'))}>{t('diag.openLogs')}</button>
      </div>
      {report && (
        <>
          <label htmlFor="diag" className="sr-only">{t('diag.report')}</label>
          <textarea id="diag" className="input diag" readOnly value={report} rows={14} />
          <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
            <span className="muted" style={{ flex: 1, fontSize: 12 }}>{t('diag.reviewHint')}</span>
            <button type="button" className="btn btn-primary" onClick={() => navigator.clipboard?.writeText(report).then(() => toast(t('diag.copied')))}>{t('common.copy')}</button>
          </div>
        </>
      )}
    </section>
  )
}
