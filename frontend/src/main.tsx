import React from 'react'
import ReactDOM from 'react-dom/client'
import App from './App'
import './styles.css'

const isMac = /Mac/i.test(navigator.platform || navigator.userAgent)
document.documentElement.classList.add(isMac ? 'platform-mac' : 'platform-other')

ReactDOM.createRoot(document.getElementById('root') as HTMLElement).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>,
)
