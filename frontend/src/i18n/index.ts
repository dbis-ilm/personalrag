import { de } from './de'
import { en } from './en'

export type Lang = 'de' | 'en'

type RawKey = keyof typeof de
// Plural keys are used without their _one/_other suffix.
export type Key = RawKey extends infer K ? (K extends `${infer B}_${'one' | 'other'}` ? B : K) : never

const dicts: Record<Lang, Record<string, string>> = { de, en }

let lang: Lang = (navigator.language || '').toLowerCase().startsWith('de') ? 'de' : 'en'

// setLang switches the UI language; the app takes it from the status the
// backend sends (settings or system language).
export function setLang(l: string | undefined) {
  if (l === 'de' || l === 'en') lang = l
  document.documentElement.lang = lang
}

export function getLang(): Lang {
  return lang
}

// t looks up a text and fills {placeholders}. With a {count} parameter it
// picks the _one/_other plural form when the key has them.
export function t(key: Key, params?: Record<string, string | number>): string {
  const d = dicts[lang]
  let s: string | undefined
  if (params && typeof params.count === 'number') {
    const form = new Intl.PluralRules(lang).select(params.count) === 'one' ? 'one' : 'other'
    s = d[`${key}_${form}`]
  }
  s ??= d[key] ?? key
  if (!params) return s
  return s.replace(/\{(\w+)\}/g, (m, name: string) => (name in params ? fmtParam(params[name]) : m))
}

function fmtParam(v: string | number): string {
  return typeof v === 'number' ? fmtNumber(v) : v
}

// locale prefers the system's regional variant of the UI language (en-GB
// dates for a British system), else a sensible default.
function locale(): string {
  const nav = navigator.language || ''
  if (nav.toLowerCase().startsWith(lang)) return nav
  return lang === 'de' ? 'de-DE' : 'en-US'
}

export function fmtNumber(n: number, digits?: number): string {
  return new Intl.NumberFormat(locale(), digits === undefined ? undefined : { minimumFractionDigits: digits, maximumFractionDigits: digits }).format(n)
}

// fmtDate formats a date without time, e.g. for a file's modification.
export function fmtDate(ms: number): string {
  if (!ms) return ''
  return new Intl.DateTimeFormat(locale(), { year: 'numeric', month: '2-digit', day: '2-digit' }).format(ms)
}

export function fmtDateTime(v: number | string): string {
  const d = typeof v === 'string' ? new Date(v) : new Date(v)
  if (Number.isNaN(d.getTime())) return String(v)
  return new Intl.DateTimeFormat(locale(), { dateStyle: 'medium', timeStyle: 'short' }).format(d)
}

// relDay labels a timestamp compactly: the time today, "yesterday", the day
// this year, else the date.
export function relDay(ms: number): string {
  if (!ms) return ''
  const d = new Date(ms)
  const now = new Date()
  const sameDay = (a: Date, b: Date) => a.getFullYear() === b.getFullYear() && a.getMonth() === b.getMonth() && a.getDate() === b.getDate()
  if (sameDay(d, now)) return new Intl.DateTimeFormat(locale(), { hour: '2-digit', minute: '2-digit' }).format(d)
  const y = new Date(now)
  y.setDate(now.getDate() - 1)
  if (sameDay(d, y)) return t('time.yesterday')
  if (d.getFullYear() === now.getFullYear()) return new Intl.DateTimeFormat(locale(), { day: '2-digit', month: '2-digit' }).format(d)
  return fmtDate(ms)
}

// fmtDuration labels a call duration: "1.4 s", "12 s", "2:05 min".
export function fmtDuration(ms: number): string {
  if (ms <= 0) return ''
  if (ms < 10_000) return `${fmtNumber(ms / 1000, 1)} s`
  const s = Math.round(ms / 1000)
  if (s < 60) return `${s} s`
  return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, '0')} min`
}

// note turns a stored document or turn note into text: "@code" notes are
// translated, anything else is shown as it is.
export function note(n: string): string {
  if (!n.startsWith('@')) return n
  const key = n === '@interrupted' ? 'ask.interrupted' : `note.${n.slice(1)}`
  return key in dicts[lang] ? t(key as Key) : n
}
