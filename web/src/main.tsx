import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
// Shipped with the app rather than fetched from a font CDN: the console runs
// inside company networks, often without internet, and should not call out.
import '@fontsource-variable/geist'
import '@fontsource-variable/geist-mono'
import './index.css'
import { Root } from './Root.tsx'

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <Root />
  </StrictMode>,
)
