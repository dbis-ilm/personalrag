import { useCallback, useEffect, useMemo, useState } from 'react'
import { Events } from '@wailsio/runtime'
import { AppService, Collection, Document, Folder } from '../../bindings/github.com/dbis-ilm/personalrag'
import Icon from '../components/Icon'
import { toast } from '../components/Toasts'
import { errorText } from '../App'
import { Key, fmtDate, note, t } from '../i18n'

const STATUS: Record<string, { label: Key; action?: Key }> = {
  ok: { label: 'status.ok', action: 'action.ok' },
  pending: { label: 'status.pending', action: 'action.pending' },
  running: { label: 'status.running' },
  error: { label: 'status.error', action: 'action.error' },
  ocr: { label: 'status.ocr' },
  cloud: { label: 'status.cloud' },
}

function folderState(f: Folder): string {
  switch (f.state) {
    case 'working': return t('folder.working', { count: f.count })
    case 'notes': return t('folder.notes', { count: f.count })
    case 'empty': return t('folder.empty')
    default: return t('folder.current')
  }
}
const FILTERS = ['', 'ok', 'pending', 'running', 'error', 'ocr']
const isMac = document.documentElement.classList.contains('platform-mac')

// MAX_NOTES matches rag.MaxInstructions; the backend caps longer text.
const MAX_NOTES = 2000

// NotesEditor edits a collection's notes for the answer model.
function NotesEditor({ c, onDone }: { c: Collection; onDone: () => void }) {
  const [text, setText] = useState(c.instructions ?? '')
  const [saving, setSaving] = useState(false)
  const id = `notes-${c.id}`
  const save = () => {
    setSaving(true)
    AppService.SetCollectionInstructions(c.id, text)
      .then(() => { toast(t('src.notesSaved', { name: c.name })); onDone() })
      .catch((e) => toast(errorText(e), 'error'))
      .finally(() => setSaving(false))
  }
  return (
    <div className="field" style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
      <label htmlFor={id} style={{ fontSize: 12, fontWeight: 600 }}>{t('src.notesLabel', { name: c.name })}</label>
      <textarea id={id} className="input" rows={4} maxLength={MAX_NOTES} value={text} onChange={(e) => setText(e.target.value)}
        style={{ padding: '8px 10px', resize: 'vertical', lineHeight: 1.45 }} aria-describedby={`${id}-help`} autoFocus />
      <span id={`${id}-help`} className="muted" style={{ fontSize: 12 }}>{t('src.notesHelp')}</span>
      <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
        <span className="muted" style={{ fontSize: 12, flex: 1, fontVariantNumeric: 'tabular-nums' }}>{t('src.notesCount', { count: text.length, max: MAX_NOTES })}</span>
        <button type="button" className="btn btn-sm" onClick={onDone}>{t('common.close')}</button>
        <button type="button" className="btn btn-sm btn-primary" disabled={saving || text === (c.instructions ?? '')} onClick={save}>{saving ? t('common.saving') : t('common.save')}</button>
      </div>
    </div>
  )
}

export default function SourcesView({ onAddFolder }: { onAddFolder: () => void }) {
  const [folders, setFolders] = useState<Folder[]>([])
  const [collections, setCollections] = useState<Collection[]>([])
  const [all, setAll] = useState<Document[]>([])
  const [filter, setFilter] = useState('')
  const [query, setQuery] = useState('')
  const [confirm, setConfirm] = useState('')
  const [notesFor, setNotesFor] = useState('') // folder path whose notes editor is open

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
    act(AppService.RemoveCollection(c.id), t('src.removed', { name: c.name }))
  }

  return (
    <div className="scroll">
      <div className="page">
        <div className="page-head">
          <div className="grow">
            <h1>{t('nav.sources')}</h1>
            <p className="lead">{t('src.lead')}</p>
          </div>
          <button type="button" className="btn" style={{ minHeight: 34 }} onClick={() => act(AppService.Rescan(), t('src.rescanning'))}>{t('src.rescan')}</button>
          <button type="button" className="btn btn-primary" style={{ minHeight: 34 }} onClick={onAddFolder}><Icon name="plus" size={14} strokeWidth={2.2} />{t('common.addFolder')}</button>
        </div>

        <section aria-label={t('src.folders')} className="folder-grid">
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
                  <span className="tag">{t('src.files', { count: f.files })}</span>
                  {c?.instructions && <span className="tag">{t('src.notesSet')}</span>}
                </div>
                <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                  <span className={`text-${f.stateKind}`} style={{ fontSize: 12, fontWeight: 600, flex: 1 }}>{folderState(f)}</span>
                  {c && notesFor !== f.path && (
                    <button type="button" className="btn btn-sm" onClick={() => setNotesFor(f.path)}>{t('src.notes')}</button>
                  )}
                  {c && (
                    <button type="button" className={`btn btn-sm${confirm === c.id ? ' btn-danger' : ''}`} onClick={() => remove(c)} onBlur={() => setConfirm('')}>
                      {confirm === c.id ? t('src.confirmRemove') : t('src.remove')}
                    </button>
                  )}
                </div>
                {c && notesFor === f.path && <NotesEditor c={c} onDone={() => setNotesFor('')} />}
              </div>
            )
          })}
          <button type="button" className="dropzone" data-file-drop-target onClick={onAddFolder}>
            <Icon name="upload" size={20} />
            {t('src.drop')}
          </button>
        </section>

        <section aria-label={t('src.documents')} style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
          <div style={{ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: 8 }}>
            <div role="group" aria-label={t('src.filter')} style={{ display: 'flex', flexWrap: 'wrap', gap: 6 }}>
              {FILTERS.filter((id) => !id || counts[id]).map((id) => (
                <button key={id || 'all'} type="button" className="filter-pill" aria-pressed={filter === id} onClick={() => setFilter(id)}>
                  {id ? t(STATUS[id].label) : t('common.all')} <span style={{ opacity: 0.7, fontVariantNumeric: 'tabular-nums' }}>{counts[id] ?? 0}</span>
                </button>
              ))}
            </div>
            <div style={{ flex: 1 }} />
            <label htmlFor="docsearch" className="sr-only">{t('src.searchDocs')}</label>
            <input id="docsearch" className="input" type="search" placeholder={t('src.searchPlaceholder')} value={query} onChange={(e) => setQuery(e.target.value)} style={{ width: 240, maxWidth: '100%' }} />
          </div>

          <div className="table-wrap">
            <table>
              <thead>
                <tr><th>{t('src.colDocument')}</th><th>{t('src.colCollection')}</th><th>{t('src.colFormat')}</th><th>{t('src.colModified')}</th><th>{t('src.colStatus')}</th><th style={{ textAlign: 'right' }}>{t('src.colActions')}</th></tr>
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
                    <td style={{ color: 'var(--text-2)', whiteSpace: 'nowrap' }}>{d.format}{d.chunks > 0 && <span className="muted"> · {t('src.chunks', { count: d.chunks })}</span>}</td>
                    <td style={{ color: 'var(--text-2)', whiteSpace: 'nowrap', fontVariantNumeric: 'tabular-nums' }}>{fmtDate(d.modTime)}</td>
                    <td>
                      <span className={`chip kind-${d.status}`}><span className="chip-dot" />{STATUS[d.status] ? t(STATUS[d.status].label) : d.status}</span>
                      {d.note && <div className="muted" style={{ fontSize: 12, marginTop: 4 }}>{note(d.note)}</div>}
                    </td>
                    <td style={{ textAlign: 'right', whiteSpace: 'nowrap' }}>
                      {STATUS[d.status]?.action && (
                        <button type="button" className="btn btn-sm" onClick={() => act(AppService.Reindex(d.fullPath), t('src.reindexing', { name: d.name }))}>{t(STATUS[d.status].action!)}</button>
                      )}{' '}
                      <button type="button" className="btn btn-sm" onClick={() => act(AppService.RevealDocument(d.fullPath))}>{isMac ? t('src.inFinder') : t('src.inExplorer')}</button>
                    </td>
                  </tr>
                ))}
                {docs.length === 0 && (
                  <tr><td colSpan={6} className="muted" style={{ textAlign: 'center', padding: 24 }}>
                    {all.length === 0 ? t('src.noDocs') : t('src.noMatch')}
                  </td></tr>
                )}
              </tbody>
            </table>
          </div>
          <p className="muted" style={{ margin: 0, fontSize: 12 }}>
            {docs.length > shown.length && `${t('src.truncated', { shown: shown.length, total: docs.length })} `}
            {t('src.supported')}
          </p>
        </section>
      </div>
    </div>
  )
}
