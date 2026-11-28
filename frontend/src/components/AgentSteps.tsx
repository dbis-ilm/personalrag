import { AgentStep } from '../../bindings/github.com/dbis-ilm/personalrag'
import { Key, fmtDuration, t } from '../i18n'
import Icon from './Icon'

const TOOLS: Record<string, Key> = {
  list_collections: 'tool.list_collections',
  list_documents: 'tool.list_documents',
  read_document: 'tool.read_document',
  search_documents: 'tool.search_documents',
  extract: 'tool.extract',
  check: 'tool.check',
  calc: 'tool.calc',
}

// inputLine shortens a tool's JSON input to what helps reading along:
// the instruction, query or document, not the whole object.
function inputLine(input: string): string {
  try {
    const v = JSON.parse(input) as Record<string, unknown>
    const pick = v.instruction ?? v.query ?? v.code ?? v.pages
    const doc = v.document_id !== undefined ? `#${v.document_id}` : v.collection_id ? String(v.collection_id) : ''
    const text = [doc, typeof pick === 'string' ? pick : ''].filter(Boolean).join(' · ')
    return text.length > 140 ? `${text.slice(0, 139)}…` : text
  } catch {
    return input.slice(0, 140)
  }
}

// AgentSteps shows what the agent did: live while it runs, folded after.
export default function AgentSteps({ steps, running, model, onStop }: { steps: AgentStep[]; running: boolean; model: string; onStop: () => void }) {
  const list = (
    <ol className="agent-steps">
      {steps.map((s, i) => (
        <li key={i} className={s.error ? 'is-error' : undefined}>
          <span className="agent-tool">{TOOLS[s.tool] ? t(TOOLS[s.tool]) : s.tool}</span>
          <span className="agent-input">{inputLine(s.input)}</span>
          <span className="agent-result">{s.error || s.summary}</span>
          <span className="agent-time">{fmtDuration(Math.max(s.millis, 1))}</span>
        </li>
      ))}
    </ol>
  )
  if (running) {
    return (
      <div className="agent-box" aria-live="polite">
        <div className="meta-line">
          <span className="spinner" aria-hidden="true" />
          <span>{t('agent.working', { model, count: steps.length })}</span>
          <div style={{ flex: 1 }} />
          <button type="button" className="btn btn-sm" onClick={onStop}><Icon name="close" size={12} strokeWidth={2.4} />{t('agent.stop')}</button>
        </div>
        {steps.length > 0 && list}
      </div>
    )
  }
  if (steps.length === 0) return null
  return (
    <details className="agent-box">
      <summary className="meta-line">{t('agent.steps', { count: steps.length })}</summary>
      {list}
    </details>
  )
}
