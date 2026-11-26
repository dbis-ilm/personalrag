import { useCallback, useEffect, useState } from 'react'
import { AppService, AppStatus, Collection, RuntimeInfo } from '../../bindings/personalrag'
import Icon from '../components/Icon'
import { toast } from '../components/Toasts'
import { errorText } from '../App'

const STEPS = ['Runtime prüfen', 'Modelle wählen', 'Ordner wählen']

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
  const [saving, setSaving] = useState(false)

  const check = useCallback(async () => {
    setChecking(true)
    try {
      const info = await AppService.RuntimeInfo()
      setRt(info)
      setEmb((e) => e || info.embedding || info.suggestedEmbedding)
      setLlm((l) => l || (mode === 'settings' && status.searchOnly && status.setupDone ? '' : info.llm || info.suggestedLlm))
    } finally {
      setChecking(false)
    }
  }, [mode, status.searchOnly, status.setupDone])

  useEffect(() => { check() }, [check])

  const models = rt?.models ?? []
  const embModels = models.filter((m) => m.embedding)
  const llmModels = models.filter((m) => !m.embedding)
  const embChanged = !!rt?.embedding && emb !== rt.embedding
  const modelsDirty = !!rt && (emb !== rt.embedding || llm !== rt.llm)

  const save = async (): Promise<boolean> => {
    setSaving(true)
    try {
      await AppService.SaveModels(llm, emb)
      toast(embChanged ? 'Modelle gespeichert. Der Index wird mit dem neuen Embedding-Modell neu aufgebaut.' : 'Modelle gespeichert', 'ok')
      setRt((r) => (r ? { ...r, llm, embedding: emb } : r))
      return true
    } catch (e) {
      toast(errorText(e), 'error')
      return false
    } finally {
      setSaving(false)
    }
  }

  const finish = () => AppService.FinishSetup().catch((e) => toast(errorText(e), 'error'))

  const runtimeBlock = (
    <>
      {rt?.ollamaOk ? (
        <div className="setup-row">
          <span className="dot" style={{ width: 10, height: 10, borderRadius: 5 }} />
          <div style={{ flex: 1, display: 'flex', flexDirection: 'column', gap: 2 }}>
            <span style={{ fontWeight: 600 }}>Ollama</span>
            <span className="muted" style={{ fontSize: 12 }}>Erreichbar unter {rt.host} · {models.length} Modelle installiert</span>
          </div>
          <span className="text-ok" style={{ fontSize: 12, fontWeight: 600 }}>Gefunden</span>
        </div>
      ) : (
        <div className="setup-row">
          <span style={{ width: 10, height: 10, borderRadius: 5, background: rt ? 'var(--err)' : '#bfc2c7' }} />
          <div style={{ flex: 1, display: 'flex', flexDirection: 'column', gap: 2 }}>
            <span style={{ fontWeight: 600 }}>Ollama</span>
            <span className="muted" style={{ fontSize: 12 }}>
              {rt ? <>{rt.error} Installiere Ollama von ollama.com und starte es.</> : 'Wird geprüft …'}
            </span>
          </div>
          <button type="button" className="btn btn-sm" onClick={check} disabled={checking}>{checking ? 'Prüfe …' : 'Erneut prüfen'}</button>
        </div>
      )}
    </>
  )

  const modelBlock = (
    <>
      <div className="field">
        <label htmlFor="emb">Embedding-Modell (für die Suche)</label>
        <select id="emb" className="input select" value={emb} onChange={(e) => setEmb(e.target.value)} disabled={!rt?.ollamaOk}>
          {embModels.length === 0 && <option value="">Kein Embedding-Modell installiert</option>}
          {embModels.map((m) => <option key={m.name} value={m.name}>{m.name} · {m.size}</option>)}
        </select>
        {rt?.ollamaOk && embModels.length === 0 && (
          <span className="muted" style={{ fontSize: 12 }}>Im Terminal installieren: <code className="inline-code">ollama pull bge-m3</code> (mehrsprachig, gut für Deutsch), dann erneut prüfen.</span>
        )}
        {embChanged && <span className="text-warn" style={{ fontSize: 12, fontWeight: 600 }}>Ein anderes Embedding-Modell baut den gesamten Index neu auf.</span>}
      </div>
      <div className="field">
        <label htmlFor="llm">Antwortmodell</label>
        <select id="llm" className="input select" value={llm} onChange={(e) => setLlm(e.target.value)} disabled={!rt?.ollamaOk}>
          <option value="">Keins: nur Suche (schnell, auch auf schwacher Hardware)</option>
          {llmModels.map((m) => <option key={m.name} value={m.name}>{m.name} · {m.size}</option>)}
        </select>
      </div>
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
          <div className="page-head"><div className="grow"><h1>Einstellungen</h1><p className="lead">Alles läuft lokal. Daten liegen in {status.dataDir}.</p></div></div>
          {status.error && <div className="inline-note error"><Icon name="alert" size={13} strokeWidth={2.2} />{status.error}</div>}
          <section className="card"><h2>Modell-Runtime</h2>{runtimeBlock}</section>
          <section className="card">
            <h2>Modelle</h2>
            {modelBlock}
            <div style={{ display: 'flex', justifyContent: 'flex-end' }}>
              <button type="button" className="btn btn-primary" disabled={!modelsDirty || !emb || saving} onClick={save}>{saving ? 'Speichere …' : 'Speichern'}</button>
            </div>
          </section>
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
        </div>
      </div>
    )
  }

  const canNext = step === 0 ? !!rt?.ollamaOk : step === 1 ? !!emb : collections.length > 0
  const next = async () => {
    if (step === 1 && (modelsDirty || !status.embedding) && !(await save())) return
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
                <h1 style={{ fontSize: 22 }}>Lokale Modell-Runtime prüfen</h1>
                <p className="lead" style={{ lineHeight: 1.55 }}>PersonalRAG bringt keine Modelle mit, sondern nutzt Ollama auf diesem Rechner. Deine Dokumente verlassen den Rechner nicht.</p>
              </div>
              {runtimeBlock}
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
