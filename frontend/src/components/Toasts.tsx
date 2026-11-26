import { useEffect, useState } from 'react'

type Toast = { id: number; text: string; kind: string }
let listener: ((t: Toast) => void) | null = null
let nextId = 1

// toast shows a short message bottom-right; kind is "ok" | "warn" | "error".
export function toast(text: string, kind = 'ok') {
  listener?.({ id: nextId++, text, kind })
}

export default function Toasts() {
  const [items, setItems] = useState<Toast[]>([])
  useEffect(() => {
    listener = (t) => {
      setItems((xs) => [...xs, t])
      setTimeout(() => setItems((xs) => xs.filter((x) => x.id !== t.id)), t.kind === 'error' ? 8000 : 4500)
    }
    return () => { listener = null }
  }, [])
  return (
    <div className="toasts" role="status" aria-live="polite">
      {items.map((t) => (
        <div key={t.id} className={`toast toast-${t.kind}`}>
          <span style={{ flex: 1 }}>{t.text}</span>
          <button type="button" aria-label="Schließen" onClick={() => setItems((xs) => xs.filter((x) => x.id !== t.id))}>×</button>
        </div>
      ))}
    </div>
  )
}
