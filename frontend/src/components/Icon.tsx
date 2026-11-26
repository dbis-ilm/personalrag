const paths: Record<string, JSX.Element> = {
  logo: <><path d="M4 4h10l6 6v10H4z" /><path d="M14 4v6h6" /><circle cx="11" cy="15" r="2.5" /><path d="M13 17l2 2" /></>,
  chat: <path d="M21 12a8 8 0 0 1-11.6 7.1L4 20l1-4.6A8 8 0 1 1 21 12z" />,
  folder: <path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z" />,
  plug: <><path d="M9 7V3M15 7V3" /><rect x="6" y="7" width="12" height="7" rx="2" /><path d="M12 14v3a4 4 0 0 1-4 4" /></>,
  gear: <><circle cx="12" cy="12" r="3" /><path d="M12 2v3M12 19v3M2 12h3M19 12h3M4.9 4.9l2.1 2.1M17 17l2.1 2.1M4.9 19.1L7 17M17 7l2.1-2.1" /></>,
  plus: <path d="M12 5v14M5 12h14" />,
  close: <path d="M6 6l12 12M18 6L6 18" />,
  filter: <path d="M4 6h16M7 12h10M10 18h4" />,
  search: <><circle cx="11" cy="11" r="7" /><path d="M20 20l-4-4" /></>,
  send: <path d="M12 19V5M5 12l7-7 7 7" />,
  check: <path d="M5 12l5 5L20 7" />,
  alert: <><path d="M12 3l9.5 17h-19z" /><path d="M12 10v4M12 17.5v.01" /></>,
  up: <path d="M7 10v11M7 10l4-7a2 2 0 0 1 3 2l-1 5h6a2 2 0 0 1 2 2.3l-1.4 7A2 2 0 0 1 17.6 21H7" />,
  down: <path d="M17 14V3M17 14l-4 7a2 2 0 0 1-3-2l1-5H5a2 2 0 0 1-2-2.3l1.4-7A2 2 0 0 1 6.4 3H17" />,
  external: <path d="M14 3h7v7M10 14L21 3M21 14v7H3V3h7" />,
  lock: <><rect x="5" y="11" width="14" height="10" rx="2" /><path d="M8 11V7a4 4 0 0 1 8 0v4" /></>,
  upload: <path d="M12 16V4M7 9l5-5 5 5M4 20h16" />,
  trash: <path d="M4 7h16M9 7V4h6v3M6 7l1 13h10l1-13" />,
  pause: <path d="M9 5v14M15 5v14" />,
  play: <path d="M7 5l12 7-12 7z" />,
  panelOpen: <><rect x="3" y="4" width="18" height="16" rx="2" /><path d="M15 4v16" /><path d="M10 10l-2 2 2 2" /></>,
  panelClose: <><rect x="3" y="4" width="18" height="16" rx="2" /><path d="M15 4v16" /><path d="M8 10l2 2-2 2" /></>,
}

export default function Icon({ name, size = 16, strokeWidth = 1.9, label }: {
  name: keyof typeof paths | string
  size?: number
  strokeWidth?: number
  label?: string
}) {
  return (
    <svg
      width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor"
      strokeWidth={strokeWidth} strokeLinecap="round" strokeLinejoin="round"
      {...(label ? { role: 'img', 'aria-label': label } : { 'aria-hidden': true })}
    >
      {paths[name]}
    </svg>
  )
}
