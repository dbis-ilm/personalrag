import { useEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { AppService, Source } from '../../bindings/github.com/dbis-ilm/personalrag'
import { errorText } from '../App'
import { fmtDate, t } from '../i18n'
import { toHTML, toMarkdown, toPlain, type ShareInput } from '../share'
import Icon from './Icon'
import { toast } from './Toasts'

// ShareMenu offers an answer with its sources for copying, e-mail,
// printing (or PDF via the print dialog) and saving as Markdown.
export default function ShareMenu({ question, answer, sources }: { question: string; answer: string; sources: Source[] }) {
  const [open, setOpen] = useState(false)
  const [printHTML, setPrintHTML] = useState('')
  const ref = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!open) return
    const close = (e: MouseEvent | KeyboardEvent) => {
      if (e instanceof KeyboardEvent ? e.key === 'Escape' : !ref.current?.contains(e.target as Node)) setOpen(false)
    }
    document.addEventListener('mousedown', close)
    document.addEventListener('keydown', close)
    return () => { document.removeEventListener('mousedown', close); document.removeEventListener('keydown', close) }
  }, [open])

  const input = (): ShareInput => ({
    question,
    answer,
    sources: sources.map((s) => ({ n: s.n, file: s.file, location: s.location, short: s.short })),
    labels: { question: t('share.question'), sources: t('share.sources'), footer: t('share.footer', { date: fmtDate(Date.now()) }) },
  })

  const run = (fn: () => Promise<void>) => () => {
    setOpen(false)
    fn().catch((e) => toast(errorText(e), 'error'))
  }

  const copy = run(async () => {
    const inp = input()
    const plain = toPlain(inp)
    try {
      await navigator.clipboard.write([new ClipboardItem({
        'text/html': new Blob([toHTML(inp)], { type: 'text/html' }),
        'text/plain': new Blob([plain], { type: 'text/plain' }),
      })])
    } catch {
      await navigator.clipboard.writeText(plain) // no rich clipboard here
    }
    toast(t('share.copied'))
  })

  const mail = run(async () => {
    const subject = t('share.subject', { question: question.length > 80 ? `${question.slice(0, 79)}…` : question })
    if (await AppService.ShareMail(subject, toPlain(input()))) toast(t('share.mailShortened'), 'warn')
  })

  const print = run(async () => {
    setPrintHTML(toHTML(input()))
    await new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r))) // let the print area render
    try {
      await AppService.Print()
    } finally {
      setPrintHTML('')
    }
  })

  const save = run(async () => {
    const path = await AppService.SaveExport(question, toMarkdown(input()))
    if (path) toast(t('share.saved', { path }))
  })

  return (
    <div className="share" ref={ref}>
      <button type="button" className="btn" aria-haspopup="menu" aria-expanded={open} onClick={() => setOpen(!open)}>
        <Icon name="external" size={14} />{t('share.button')}
      </button>
      {open && (
        <div className="share-menu" role="menu">
          <button type="button" role="menuitem" onClick={copy}>{t('share.copy')}</button>
          <button type="button" role="menuitem" onClick={mail}>{t('share.mail')}</button>
          <button type="button" role="menuitem" onClick={print}>{t('share.print')}</button>
          <button type="button" role="menuitem" onClick={save}>{t('share.save')}</button>
        </div>
      )}
      {printHTML && createPortal(<div className="print-area" dangerouslySetInnerHTML={{ __html: printHTML }} />, document.body)}
    </div>
  )
}
