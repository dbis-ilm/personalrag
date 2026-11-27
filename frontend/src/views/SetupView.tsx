import { useCallback, useEffect, useState } from 'react'
import { AppService, AppStatus, Collection, ModelInfo, ProviderInfo, RuntimeInfo } from '../../bindings/github.com/dbis-ilm/personalrag'
import Icon from '../components/Icon'
import { toast } from '../components/Toasts'
import { errorText } from '../App'

const STEPS = ['Modelle verbinden', 'Modelle wählen', 'Ordner wählen']

// Setup wizard on first start; the same building blocks as the settings page.
export default function SetupView({ mode, status, collections, onAddFolder, onHistoryCleared }: {
  mode: 'wizard' | 'settings'
  status: AppStatus
  collections: Collection[]
  onAddFolder: () => void
  onHistoryCleared?: () => void
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
      toast(embChanged ? 'Modelle gespeichert. Der Index wird mit dem neuen Embedding-Modell neu aufgebaut.' : 'Modelle gespeichert', 'ok')
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
        <label htmlFor="emb">Embedding-Modell (für die Suche)</label>
        <select id="emb" className="input select" value={emb} onChange={(e) => setEmb(e.target.value)} disabled={!anyOK}>
          {!providers.some((p) => p.models?.some((m) => m.embedding)) && !emb && <option value="">Kein Embedding-Modell gefunden</option>}
          {providers.filter((p) => p.ok).map((p) => (
            <ModelOptions key={p.id} provider={p} models={(p.models ?? []).filter((m) => m.embedding || p.id === 'gpustack')} />
          ))}
        </select>
        {ollama?.ok && !ollama.models?.some((m) => m.embedding) && (
          <span className="muted" style={{ fontSize: 12 }}>Lokal installieren: <code className="inline-code">ollama pull bge-m3</code> (mehrsprachig, gut für Deutsch), dann erneut prüfen.</span>
        )}
        {gpu?.ok && <span className="muted" style={{ fontSize: 12 }}>GPUStack sagt nicht, welches Modell Embeddings kann; beim Speichern wird das gewählte Modell geprüft.</span>}
        {embChanged && <span className="text-warn" style={{ fontSize: 12, fontWeight: 600 }}>Ein anderes Embedding-Modell baut den gesamten Index neu auf.</span>}
      </div>
      <div className="field">
        <label htmlFor="llm">Antwortmodell</label>
        <select id="llm" className="input select" value={llm} onChange={(e) => setLlm(e.target.value)} disabled={!anyOK}>
          <option value="">Keins: nur Suche (schnell, auch auf schwacher Hardware)</option>
          {providers.filter((p) => p.ok).map((p) => (
            <ModelOptions key={p.id} provider={p} models={(p.models ?? []).filter((m) => !m.embedding)} />
          ))}
        </select>
      </div>
      {usesRemote && gpu && (needsConsent ? (
        <div className="consent">
          <Icon name="alert" size={16} strokeWidth={2.2} />
          <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
            <strong>Textauszüge verlassen diesen Rechner</strong>
            <span>Mit diesem Modell gehen deine Fragen und Textauszüge aus deinen Dokumenten an <code className="inline-code">{gpu.host}</code>. Wer diesen Server betreibt, kann sie sehen.</span>
            <label className="check">
              <input type="checkbox" checked={consent} onChange={(e) => setConsent(e.target.checked)} />
              Einverstanden: Fragen und Textauszüge dürfen an {gpu.host} gesendet werden
            </label>
          </div>
        </div>
      ) : (
        <span className="muted" style={{ fontSize: 12 }}>Freigegeben: Fragen und Textauszüge gehen an {gpu.host}.</span>
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
              <span className="muted" style={{ fontSize: 12 }}>{c.docCount} Dateien</span>
            </div>
          ))}
        </div>
      )}
      <button type="button" className="dropzone" data-file-drop-target onClick={onAddFolder} style={{ padding: 22 }}>
        <Icon name="folder" size={24} strokeWidth={1.6} />
        <span style={{ fontWeight: 600, color: 'var(--text)' }}>{collections.length ? 'Weiteren Ordner hinzufügen' : 'Ordner auswählen oder hierher ziehen'}</span>
        <span style={{ fontSize: 12 }}>PDF, Word, Markdown, Text und HTML · Dateien werden nie verändert</span>
      </button>
    </>
  )

  if (mode === 'settings') {
    return (
      <div className="scroll">
        <div className="page" style={{ maxWidth: 760 }}>
          <div className="page-head"><div className="grow"><h1>Einstellungen</h1><p className="lead">{status.remote ? `Modelle teilweise auf ${status.remote}.` : 'Alles läuft lokal.'} Daten liegen in {status.dataDir}.</p></div></div>
          {status.error && <div className="inline-note error"><Icon name="alert" size={13} strokeWidth={2.2} />{status.error}</div>}
          <section className="card"><h2>Verbindungen</h2>{connectionBlock}</section>
          <section className="card">
            <h2>Modelle</h2>
            {modelBlock}
            <div style={{ display: 'flex', justifyContent: 'flex-end' }}>
              <button type="button" className="btn btn-primary" disabled={!modelsDirty && !needsConsent || !emb || saving || (needsConsent && !consent)} onClick={saveModels}>{saving ? 'Speichere …' : 'Speichern'}</button>
            </div>
          </section>
          <ExclusionsCard />
          <section className="card"><h2>Ordner</h2>{folderBlock}</section>
          <section className="card">
            <h2>Verlauf</h2>
            <p className="muted" style={{ margin: 0, fontSize: 13 }}>Unterhaltungen werden mit Fragen, Antworten und zitierten Textauszügen im lokalen Index gespeichert.</p>
            <div>
              <button
                type="button" className={`btn${confirmClear ? ' btn-danger' : ''}`} onBlur={() => setConfirmClear(false)}
                onClick={async () => {
                  if (!confirmClear) { setConfirmClear(true); return }
                  setConfirmClear(false)
                  try {
                    await AppService.DeleteAllConversations()
                    toast('Verlauf gelöscht')
                    onHistoryCleared?.()
                  } catch (e) {
                    toast(errorText(e), 'error')
                  }
                }}
              >
                {confirmClear ? 'Wirklich alle Unterhaltungen löschen?' : 'Verlauf löschen'}
              </button>
            </div>
          </section>
          <DiagnosticsCard lastCrash={status.lastCrash} />
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
          <span className="brand-name" style={{ fontSize: 17 }}>PersonalRAG einrichten</span>
        </div>
        <ol className="steps" aria-label="Einrichtungsschritte">
          {STEPS.map((label, i) => (
            <li key={label} className={`step${i === step ? ' is-current' : ''}${i < step ? ' is-done' : ''}`} aria-current={i === step ? 'step' : undefined}>
              <span className="step-dot">{i < step ? <Icon name="check" size={13} strokeWidth={2.6} /> : i + 1}</span>
              {label}
            </li>
          ))}
        </ol>

        <div className="card" style={{ width: '100%', maxWidth: 680, padding: 32, gap: 18 }}>
          {step === 0 && (
            <>
              <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
                <h1 style={{ fontSize: 22 }}>Modelle verbinden</h1>
                <p className="lead" style={{ lineHeight: 1.55 }}>PersonalRAG bringt keine Modelle mit. Es nutzt Ollama auf diesem Rechner oder einen GPUStack-Server. Mit Ollama verlassen deine Dokumente den Rechner nicht.</p>
              </div>
              {connectionBlock}
            </>
          )}
          {step === 1 && (
            <>
              <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
                <h1 style={{ fontSize: 22 }}>Modelle wählen</h1>
                <p className="lead" style={{ lineHeight: 1.55 }}>Das Embedding-Modell macht Texte durchsuchbar, das Antwortmodell formuliert belegte Antworten. Ohne Antwortmodell bleibt die Suche voll nutzbar.</p>
              </div>
              {modelBlock}
            </>
          )}
          {step === 2 && (
            <>
              <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
                <h1 style={{ fontSize: 22 }}>Ersten Ordner wählen</h1>
                <p className="lead" style={{ lineHeight: 1.55 }}>Jeder Ordner wird zu einer Sammlung und automatisch nachgeführt. Die Indexierung läuft im Hintergrund weiter.</p>
              </div>
              {folderBlock}
            </>
          )}

          <div style={{ display: 'flex', alignItems: 'center', gap: 10, paddingTop: 8, borderTop: '1px solid var(--line-soft)' }}>
            {step > 0 && <button type="button" className="btn" style={{ minHeight: 40, padding: '0 16px' }} onClick={() => setStep(step - 1)}>Zurück</button>}
            <div style={{ flex: 1 }} />
            {step < STEPS.length - 1
              ? <button type="button" className="btn btn-primary" style={{ minHeight: 40, padding: '0 18px' }} disabled={!canNext || saving} onClick={next}>{saving ? 'Speichere …' : 'Weiter'}</button>
              : <button type="button" className="btn btn-primary" style={{ minHeight: 40, padding: '0 18px' }} disabled={!canNext} onClick={finish}>Fertig</button>}
          </div>
        </div>
      </div>
    </div>
  )
}

function ModelOptions({ provider, models }: { provider: ProviderInfo; models: ModelInfo[] }) {
  if (models.length === 0) return null
  const where = provider.remote ? `extern: ${provider.host}` : 'lokal'
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
      toast('Verbindungen gespeichert', 'ok')
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
          <label htmlFor="ollama-host" className="sr-only">Ollama-Adresse</label>
          <input id="ollama-host" className="input" value={ollamaHost} onChange={(e) => setOllamaHost(e.target.value)} placeholder="http://localhost:11434" spellCheck={false} />
          <button type="button" className="btn" onClick={() => test('ollama')}>Testen</button>
        </div>
      </div>

      {!showGpu ? (
        <button type="button" className="btn" style={{ alignSelf: 'flex-start' }} onClick={() => setShowGpu(true)}>
          <Icon name="plus" size={14} strokeWidth={2.2} />GPUStack-Server verbinden (optional)
        </button>
      ) : (
        <div className="conn">
          <ProviderStatus title="GPUStack" info={tests.gpustack ?? (gpuUrl ? gpu : undefined)} checking={checking} emptyText="Nicht eingerichtet" />
          <div className="field">
            <label htmlFor="gpu-url">API-Adresse</label>
            <input id="gpu-url" className="input" value={gpuUrl} onChange={(e) => setGpuUrl(e.target.value)} placeholder="https://gpustack.example/v1" spellCheck={false} />
            <span className="muted" style={{ fontSize: 12 }}>Die OpenAI-kompatible Adresse, wie GPUStack sie anzeigt. LM Studio und llama.cpp funktionieren genauso.</span>
          </div>
          <div className="field">
            <label htmlFor="gpu-key">API-Schlüssel</label>
            <input id="gpu-key" className="input" type="password" autoComplete="off" value={gpuKey} disabled={removeKey}
              onChange={(e) => setGpuKey(e.target.value)} placeholder={keySet ? 'Gespeichert, leer lassen zum Beibehalten' : 'Optional'} />
            <span className="muted" style={{ fontSize: 12 }}>Wird im Schlüsselbund des Systems gespeichert, nicht in einer Datei.</span>
            {keySet && (
              <label className="check">
                <input type="checkbox" checked={removeKey} onChange={(e) => setRemoveKey(e.target.checked)} />
                Gespeicherten Schlüssel entfernen
              </label>
            )}
          </div>
          <div className="conn-row" style={{ justifyContent: 'flex-start' }}>
            <button type="button" className="btn" onClick={() => test('gpustack')} disabled={!gpuUrl}>Testen</button>
          </div>
        </div>
      )}

      <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
        <button type="button" className="btn" onClick={onRecheck} disabled={checking}>{checking ? 'Prüfe …' : 'Erneut prüfen'}</button>
        <div style={{ flex: 1 }} />
        <button type="button" className="btn btn-primary" onClick={save} disabled={!dirty || saving}>{saving ? 'Speichere …' : 'Verbindungen speichern'}</button>
      </div>
    </div>
  )
}

function ProviderStatus({ title, info, checking, emptyText }: { title: string; info?: ProviderInfo; checking: boolean; emptyText?: string }) {
  let dot = '#bfc2c7'
  let text = checking ? 'Wird geprüft …' : emptyText ?? 'Wird geprüft …'
  if (info && info.configured) {
    if (info.ok) {
      dot = 'var(--ok-dot)'
      text = `Erreichbar unter ${info.url} · ${info.models?.length ?? 0} Modelle${info.remote ? ' · anderer Rechner' : ''}`
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
      const t = (ex.patterns ?? []).join('\n')
      setText(t)
      setSaved(t)
      setBuiltin(ex.builtin ?? [])
    }).catch(console.error)
  }, [])

  const save = async () => {
    setSaving(true)
    try {
      await AppService.SaveExclusions(text.split('\n'))
      const ex = await AppService.Exclusions()
      const t = (ex.patterns ?? []).join('\n')
      setText(t)
      setSaved(t)
      toast('Ausschlüsse gespeichert, Ordner werden neu abgeglichen', 'ok')
    } catch (e) {
      toast(errorText(e), 'error')
    } finally {
      setSaving(false)
    }
  }

  return (
    <section className="card">
      <h2>Ausschlüsse</h2>
      <div className="field">
        <label htmlFor="excl">Datei- und Ordnernamen, die nicht indexiert werden (einer pro Zeile)</label>
        <textarea id="excl" className="input" rows={4} value={text} onChange={(e) => setText(e.target.value)} placeholder={'Archiv\n*.bak\nEntwurf*'} spellCheck={false}
          style={{ padding: '8px 12px', fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Consolas, monospace', fontSize: 12.5, resize: 'vertical' }} />
        <span className="muted" style={{ fontSize: 12 }}>Platzhalter * und ? sind erlaubt, Groß-/Kleinschreibung zählt nicht. Bereits indexierte Treffer werden beim Abgleich entfernt.</span>
        <span className="muted" style={{ fontSize: 12 }}>Immer ausgeschlossen: {builtin.join(' · ')}</span>
      </div>
      <div style={{ display: 'flex', justifyContent: 'flex-end' }}>
        <button type="button" className="btn btn-primary" onClick={save} disabled={saving || text === saved}>{saving ? 'Speichere …' : 'Speichern'}</button>
      </div>
    </section>
  )
}

const LEVELS: { id: string; label: string }[] = [
  { id: 'error', label: 'Nur Fehler' },
  { id: 'warn', label: 'Warnungen' },
  { id: 'info', label: 'Normal (empfohlen)' },
  { id: 'debug', label: 'Ausführlich (mit Fragen und Textauszügen)' },
]

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
      toast(v === 'debug' ? 'Ausführliches Protokoll an: Fragen und Textauszüge landen im Protokoll.' : 'Protokollstufe gespeichert', v === 'debug' ? 'warn' : 'ok')
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
      <h2>Diagnose</h2>
      {lastCrash && (
        <div className="inline-note error"><Icon name="alert" size={13} strokeWidth={2.2} />PersonalRAG wurde am {lastCrash} unerwartet beendet. Details stehen im Bericht.</div>
      )}
      <div className="field">
        <label htmlFor="loglevel">Protokoll</label>
        <select id="loglevel" className="input select" value={level} onChange={(e) => changeLevel(e.target.value)}>
          {LEVELS.map((l) => <option key={l.id} value={l.id}>{l.label}</option>)}
        </select>
        <span className="muted" style={{ fontSize: 12 }}>Das Protokoll bleibt auf diesem Rechner. Fragen und Dokumenttext stehen nur bei „Ausführlich“ darin.</span>
      </div>
      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 8 }}>
        <button type="button" className="btn" onClick={show} disabled={loading}>{loading ? 'Erstelle …' : report ? 'Bericht aktualisieren' : 'Diagnosebericht anzeigen'}</button>
        <button type="button" className="btn" onClick={() => AppService.RevealLogs().catch((e) => toast(errorText(e), 'error'))}>Protokollordner öffnen</button>
      </div>
      {report && (
        <>
          <label htmlFor="diag" className="sr-only">Diagnosebericht</label>
          <textarea id="diag" className="input diag" readOnly value={report} rows={14} />
          <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
            <span className="muted" style={{ flex: 1, fontSize: 12 }}>Prüfe den Bericht, bevor du ihn weitergibst. Dein Benutzerordner ist durch ~ ersetzt.</span>
            <button type="button" className="btn btn-primary" onClick={() => navigator.clipboard?.writeText(report).then(() => toast('Bericht kopiert'))}>Kopieren</button>
          </div>
        </>
      )}
    </section>
  )
}
