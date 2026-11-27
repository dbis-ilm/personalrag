import { useEffect, useRef, useState } from 'react'
import { AboutInfo, AppService } from '../../bindings/github.com/dbis-ilm/personalrag'
import appIcon from '../assets/appicon.png'
import { t } from '../i18n'
import { toast } from './Toasts'

const OS: Record<string, string> = { darwin: 'macOS', windows: 'Windows', linux: 'Linux' }

// AboutDialog: version, build, components, models in use, links. A native
// <dialog> gives focus handling and Esc for free.
export default function AboutDialog({ open, onClose }: { open: boolean; onClose: () => void }) {
  const ref = useRef<HTMLDialogElement>(null)
  const [info, setInfo] = useState<AboutInfo | null>(null)

  useEffect(() => {
    const d = ref.current
    if (!d) return
    if (open && !d.open) {
      AppService.About().then(setInfo).catch(console.error)
      d.showModal()
    } else if (!open && d.open) d.close()
  }, [open])

  const build = info ? `${info.version}${info.commit ? ` (${info.commit})` : ` (${t('about.devBuild')})`}` : ''
  const rows: [string, string][] = info ? [
    [t('about.version'), build],
    [t('about.answerModel'), info.llm || t('about.searchOnly')],
    [t('about.embeddingModel'), info.embedding || '–'],
    [t('about.models'), info.remote ? t('about.remote', { host: info.remote }) : t('about.local')],
    ['askyourdocs', info.askyourdocs],
    ['Wails', info.wails],
    ['Go', info.go.replace(/^go/, '')],
    [t('about.system'), `${OS[info.os] ?? info.os} · ${info.arch}`],
    [t('about.data'), info.dataDir],
  ] : []

  const copy = () => {
    const text = [`PersonalRAG ${build}`, ...rows.slice(1).map(([k, v]) => `${k}: ${v}`)].join('\n')
    navigator.clipboard?.writeText(text).then(() => toast(t('about.copied')))
  }
  const openPage = (page: string) => AppService.OpenRepository(page).catch((e) => toast(String(e), 'error'))

  return (
    <dialog ref={ref} className="about" onClose={onClose} onClick={(e) => { if (e.target === ref.current) onClose() }} aria-labelledby="about-title">
      <div className="about-body">
        <img src={appIcon} alt="" width={88} height={88} />
        <h1 id="about-title">PersonalRAG</h1>
        <p className="lead">{t('about.tagline')}</p>
        <dl className="about-facts">
          {rows.map(([k, v]) => <div key={k}><dt>{k}</dt><dd>{v}</dd></div>)}
        </dl>
        <div className="about-links">
          <button type="button" className="btn btn-sm" onClick={() => openPage('')}>GitHub</button>
          <button type="button" className="btn btn-sm" onClick={() => openPage('blob/main/LICENSE')}>{t('about.license')}</button>
          <button type="button" className="btn btn-sm" onClick={() => openPage('security/policy')}>{t('about.security')}</button>
        </div>
        <p className="muted about-legal">© 2026 Kai-Uwe Sattler · Apache-2.0 · {t('about.builtWith')}</p>
        <div className="about-actions">
          <button type="button" className="btn" onClick={copy} disabled={!info}>{t('about.copy')}</button>
          <button type="button" className="btn btn-primary" onClick={onClose} autoFocus>{t('common.close')}</button>
        </div>
      </div>
    </dialog>
  )
}
