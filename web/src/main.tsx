import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './index.css'
import App from './App.tsx'
import { ThemeProvider } from './ThemeProvider'
import { DensityProvider } from './DensityProvider'

// Register service worker for PWA
if ('serviceWorker' in navigator) {
  window.addEventListener('load', () => {
    navigator.serviceWorker.register('/sw.js').catch(() => {
      // SW registration failed — non-critical, continue
    })
  })
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <ThemeProvider>
      <DensityProvider>
        <App />
      </DensityProvider>
    </ThemeProvider>
  </StrictMode>,
)
