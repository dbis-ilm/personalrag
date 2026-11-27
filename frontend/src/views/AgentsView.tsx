import { useEffect, useState } from 'react'
import { AppService, AccessLogEntry, ClientConfig, Collection, MCPStatus } from '../../bindings/github.com/dbis-ilm/personalrag'
import { toast } from '../components/Toasts'
import { errorText } from '../App'
import { fmtDuration, relDay, t } from '../i18n'

export default function AgentsView({ collections, onChanged }: {
  collections: Collection[]
  onChanged: () => void
}) {
  const [status, setStatus] = useState<MCPStatus | null>(null)
  const [clients, setClients] = useState<ClientConfig[]>([])
  const [log, setLog] = useState<AccessLogEntry[]>([])
  const [tab, setTab] = useState(0)
  const [copied, setCopied] = useState(false)

  useEffect(() => {
    AppService.MCPStatus().then(setStatus).catch(console.error)
    AppService.ClientConfigs().then((v) => setClients(v ?? [])).catch(console.error)
    AppService.AccessLog().then((v) => setLog(v ?? [])).catch(console.error)
  }, [])

  const toggle = async (c: Collection) => {
    try {
      await AppService.SetCollectionShared(c.id, !c.sharedWithAgents)
      onChanged()
    } catch (e) {
      toast(errorText(e), 'error')
    }
  }

  const copy = async () => {
    const text = clients[tab]?.snippet
    if (!text) return
    await navigator.clipboard?.writeText(text)
    setCopied(true)
    setTimeout(() => setCopied(false), 1500)
  }

  const client = clients[tab]

  return (
    <div className="scroll">
      <div className="page">
        <div className="page-head" style={{ alignItems: 'flex-start' }}>
          <div className="grow">
            <h1>{t('nav.agents')}</h1>
            <p className="lead">{t('agents.lead')}</p>
          </div>
          {status && (
            <div className="status-pill" style={{ background: status.running ? 'var(--ok-bg)' : 'var(--info-bg)' }}>
              <span className="dot" style={{ width: 9, height: 9, borderRadius: 5, background: status.running ? 'var(--ok-dot)' : '#9a9ea6' }} />
              <span className={status.running ? 'text-ok' : 'text-info'} style={{ fontSize: 13, fontWeight: 650 }}>{status.running ? t('agents.ready') : t('agents.unavailable')}</span>
            </div>
          )}
        </div>

        {status && <p className="lead" style={{ marginTop: -8 }}>{status.sharedCount === 0 ? t('agents.none') : t('agents.shared', { count: status.sharedCount })}</p>}

        <div className="grid-2">
          <section className="card" aria-labelledby="h-connect">
            <h2 id="h-connect">{t('agents.connect')}</h2>
            <div className="tabs" role="tablist" aria-label="Client">
              {clients.map((c, i) => (
                <button key={c.name} type="button" role="tab" aria-selected={tab === i} onClick={() => setTab(i)}>{c.name}</button>
              ))}
            </div>
            {client && (
              <>
                <p style={{ margin: 0, fontSize: 13, color: 'var(--text-2)' }}>{client.hint}</p>
                <div className="snippet">
                  <pre>{client.snippet}</pre>
                  <button type="button" className="btn btn-sm" onClick={copy}>{copied ? t('common.copied') : t('common.copy')}</button>
                </div>
              </>
            )}
          </section>

          <section className="card" aria-labelledby="h-share" style={{ gap: 8 }}>
            <h2 id="h-share">{t('agents.sharing')}</h2>
            <p className="muted" style={{ margin: 0, fontSize: 13 }}>{t('agents.sharingLead')}</p>
            {collections.length === 0 && <p className="muted" style={{ margin: 0, fontSize: 13 }}>{t('agents.noCollections')}</p>}
            {collections.map((c) => (
              <div key={c.id} className="share-row">
                <span className="swatch" style={{ background: c.color }} />
                <span style={{ flex: 1 }}>{c.name}</span>
                <span className="muted" style={{ fontSize: 12 }}>{c.sharedWithAgents ? t('agents.isShared') : t('agents.private')}</span>
                <button type="button" role="switch" className="switch" aria-checked={c.sharedWithAgents} aria-label={t('agents.shareToggle', { name: c.name })} onClick={() => toggle(c)} />
              </div>
            ))}
          </section>
        </div>

        <section aria-labelledby="h-log" style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
          <h2 id="h-log">{t('agents.log')}</h2>
          <div className="table-wrap">
            <table>
              <thead><tr><th>{t('agents.colTime')}</th><th>{t('agents.colClient')}</th><th>{t('agents.colTool')}</th><th>{t('agents.colRequest')}</th><th>{t('agents.colResult')}</th><th style={{ textAlign: 'right' }}>{t('agents.colDuration')}</th></tr></thead>
              <tbody>
                {log.map((l, i) => (
                  <tr key={i}>
                    <td style={{ whiteSpace: 'nowrap', fontVariantNumeric: 'tabular-nums', color: 'var(--text-2)' }}>{relDay(l.at)}</td>
                    <td style={{ whiteSpace: 'nowrap' }}>{l.client || t('agents.unknownClient')}</td>
                    <td><code className="inline-code">{l.tool}</code></td>
                    <td style={{ color: 'var(--text-2)' }}>{l.query}</td>
                    <td className={`text-${l.kind}`} style={{ fontWeight: 600 }}>{l.result}</td>
                    <td style={{ whiteSpace: 'nowrap', textAlign: 'right', fontVariantNumeric: 'tabular-nums', color: 'var(--text-2)' }}>{fmtDuration(l.durationMs)}</td>
                  </tr>
                ))}
                {log.length === 0 && <tr><td colSpan={6} className="muted" style={{ textAlign: 'center', padding: 24 }}>{t('agents.noAccess')}</td></tr>}
              </tbody>
            </table>
          </div>
        </section>
      </div>
    </div>
  )
}
