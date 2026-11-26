import { useCallback, useEffect, useMemo, useState } from 'react'
import { Events } from '@wailsio/runtime'
import { AppService, Collection, Document, Folder } from '../../bindings/personalrag'
import Icon from '../components/Icon'
import { toast } from '../components/Toasts'
import { errorText } from '../App'

const STATUS: Record<string, { label: string; action?: string }> = {
  ok: { label: 'Indexiert', action: 'Neu indexieren' },
  pending: { label: 'Wartend', action: 'Vorziehen' },
  running: { label: 'Wird indexiert' },
  error: { label: 'Fehler', action: 'Erneut versuchen' },
  ocr: { label: 'Braucht OCR' },
  cloud: { label: 'Nicht lokal' },
}
const FILTERS = ['', 'ok', 'pending', 'running', 'error', 'ocr']
const isMac = document.documentElement.classList.contains('platform-mac')

export default function SourcesView({ onAddFolder }: { onAddFolder: () => void }) {
  const [folders, setFolders] = useState<Folder[]>([])
  const [collections, setCollections] = useState<Collection[]>([])
  const [all, setAll] = useState<Document[]>([])
  const [filter, setFilter] = useState('')
  const [query, setQuery] = useState('')
  const [confirm, setConfirm] = useState('')

  const reload = useCallback(() => {
    AppService.Folders().then((v) => setFolders(v ?? [])).catch(console.error)
    AppService.Collections().then((v) => setCollections(v ?? [])).catch(console.error)
    AppService.Documents('').then((v) => setAll(v ?? [])).catch(console.error)
  }, [])

  useEffect(() => {
    reload()
    const off = Events.On('sources:changed', reload)
    return () => off()
  }, [reload])

  const counts = useMemo(() => {
    const c: Record<string, number> = { '': all.length }
    for (const d of all) c[d.status] = (c[d.status] ?? 0) + 1
    return c
  }, [all])

  const q = query.trim().toLowerCase()
  const docs = all.filter((d) => (!filter || d.status === filter) && (!q || `${d.name} ${d.dir}`.toLowerCase().includes(q)))
  const shown = docs.slice(0, 500)

  const act = (p: Promise<void>, ok?: string) => p.then(() => ok && toast(ok)).catch((e) => toast(errorText(e), 'error'))

  const remove = (c: Collection) => {
    if (confirm !== c.id) { setConfirm(c.id); return }
    setConfirm('')
    act(AppService.RemoveCollection(c.id), `„${c.name}“ aus dem Index entfernt. Die Dateien bleiben unverändert.`)
  }

  return (
    <div className="scroll">
      <div className="page">
        <div className="page-head">
          <div className="grow">
            <h1>Quellen</h1>
            <p className="lead">Überwachte Ordner und alle indexierten Dokumente. Änderungen werden beim Start und alle 15 Minuten abgeglichen.</p>
          </div>
          <button type="button" className="btn" style={{ minHeight: 34 }} onClick={() => act(AppService.Rescan(), 'Ordner werden geprüft')}>Jetzt abgleichen</button>
          <button type="button" className="btn btn-primary" style={{ minHeight: 34 }} onClick={onAddFolder}><Icon name="plus" size={14} strokeWidth={2.2} />Ordner hinzufügen</button>
        </div>

        <section aria-label="Überwachte Ordner" className="folder-grid">
          {folders.map((f) => {
            const c = collections.find((x) => x.name === f.collection)
            return (
              <div key={f.path} className="folder">
                <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
                  <span style={{ display: 'inline-flex', color: c?.color }}><Icon name="folder" size={18} /></span>
                  <span style={{ fontWeight: 600, overflowWrap: 'anywhere', flex: 1 }}>{f.path}</span>
                </div>
                <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6 }}>
                  <span className="tag">{f.collection}</span>
                  <span className="tag">{f.files} Dateien</span>
                </div>
                <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                  <span className={`text-${f.stateKind}`} style={{ fontSize: 12, fontWeight: 600, flex: 1 }}>{f.state}</span>
                  {c && (
                    <button type="button" className={`btn btn-sm${confirm === c.id ? ' btn-danger' : ''}`} onClick={() => remove(c)} onBlur={() => setConfirm('')}>
                      {confirm === c.id ? 'Wirklich entfernen?' : 'Entfernen'}
                    </button>
                  )}
                </div>
              </div>
            )
          })}
          <button type="button" className="dropzone" data-file-drop-target onClick={onAddFolder}>
            <Icon name="upload" size={20} />
            Ordner hierher ziehen oder klicken
          </button>
        </section>

        <section aria-label="Dokumente" style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
          <div style={{ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: 8 }}>
            <div role="group" aria-label="Nach Status filtern" style={{ display: 'flex', flexWrap: 'wrap', gap: 6 }}>
              {FILTERS.filter((id) => !id || counts[id]).map((id) => (
                <button key={id || 'all'} type="button" className="filter-pill" aria-pressed={filter === id} onClick={() => setFilter(id)}>
                  {id ? STATUS[id].label : 'Alle'} <span style={{ opacity: 0.7, fontVariantNumeric: 'tabular-nums' }}>{counts[id] ?? 0}</span>
                </button>
              ))}
            </div>
            <div style={{ flex: 1 }} />
            <label htmlFor="docsearch" className="sr-only">Dokumente durchsuchen</label>
            <input id="docsearch" className="input" type="search" placeholder="Dateiname oder Pfad …" value={query} onChange={(e) => setQuery(e.target.value)} style={{ width: 240, maxWidth: '100%' }} />
          </div>

          <div className="table-wrap">
            <table>
              <thead>
                <tr><th>Dokument</th><th>Sammlung</th><th>Format</th><th>Geändert</th><th>Status</th><th style={{ textAlign: 'right' }}>Aktionen</th></tr>
              </thead>
              <tbody>
                {shown.map((d) => (
                  <tr key={d.fullPath}>
                    <td>
                      <div style={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
                        <span style={{ fontWeight: 600, overflowWrap: 'anywhere' }}>{d.name}</span>
                        <span className="muted" style={{ fontSize: 12, overflowWrap: 'anywhere' }}>{d.dir}</span>
                      </div>
                    </td>
                    <td>{d.collection}</td>
                    <td style={{ color: 'var(--text-2)', whiteSpace: 'nowrap' }}>{d.format}{d.chunks > 0 && <span className="muted"> · {d.chunks} Abschn.</span>}</td>
                    <td style={{ color: 'var(--text-2)', whiteSpace: 'nowrap', fontVariantNumeric: 'tabular-nums' }}>{d.modified}</td>
                    <td>
                      <span className={`chip kind-${d.status}`}><span className="chip-dot" />{STATUS[d.status]?.label ?? d.status}</span>
                      {d.note && <div className="muted" style={{ fontSize: 12, marginTop: 4 }}>{d.note}</div>}
                    </td>
                    <td style={{ textAlign: 'right', whiteSpace: 'nowrap' }}>
                      {STATUS[d.status]?.action && (
                        <button type="button" className="btn btn-sm" onClick={() => act(AppService.Reindex(d.fullPath), `${d.name} wird indexiert`)}>{STATUS[d.status].action}</button>
                      )}{' '}
                      <button type="button" className="btn btn-sm" onClick={() => act(AppService.RevealDocument(d.fullPath))}>{isMac ? 'Im Finder' : 'Im Explorer'}</button>
                    </td>
                  </tr>
                ))}
                {docs.length === 0 && (
                  <tr><td colSpan={6} className="muted" style={{ textAlign: 'center', padding: 24 }}>
                    {all.length === 0 ? 'Noch keine Dokumente. Füge einen Ordner hinzu.' : 'Keine Dokumente für diesen Filter.'}
                  </td></tr>
                )}
              </tbody>
            </table>
          </div>
          <p className="muted" style={{ margin: 0, fontSize: 12 }}>
            {docs.length > shown.length && `Es werden die ersten ${shown.length} von ${docs.length} Dokumenten gezeigt. `}
            Unterstützt: PDF, Word (.docx), Markdown, Text, HTML. Ausgeschlossen: versteckte Dateien und Ordner, node_modules, Office-Sperrdateien, Dateien über 200 MB.
          </p>
        </section>
      </div>
    </div>
  )
}
