// Shell-like history for the question field: Up recalls earlier questions,
// Down goes back towards the draft the user had started.

export type HistoryState = {
  list: string[] // earlier questions, newest first
  index: number // -1: editing the draft; otherwise list[index] is shown
  saved: string // the draft, kept while paging through the history
}

export const emptyHistory = (list: string[] = []): HistoryState => ({ list, index: -1, saved: '' })

// navigate returns the next state and the text to show, or null when there
// is nothing further in that direction (the key then keeps its usual job).
export function navigate(st: HistoryState, dir: 'up' | 'down', draft: string): { state: HistoryState; text: string } | null {
  if (dir === 'up') {
    if (st.index + 1 >= st.list.length) return null
    const index = st.index + 1
    return { state: { ...st, index, saved: st.index === -1 ? draft : st.saved }, text: st.list[index] }
  }
  if (st.index < 0) return null
  const index = st.index - 1
  return { state: { ...st, index }, text: index < 0 ? st.saved : st.list[index] }
}

// remember puts a question just asked at the front, once.
export function remember(st: HistoryState, question: string, max = 200): HistoryState {
  return emptyHistory([question, ...st.list.filter((q) => q !== question)].slice(0, max))
}

// caretAllows reports whether Up/Down should page the history rather than
// move the caret: Up only from the first line, Down only from the last.
export function caretAllows(value: string, start: number, end: number, dir: 'up' | 'down'): boolean {
  return dir === 'up' ? !value.slice(0, start).includes('\n') : !value.slice(end).includes('\n')
}
