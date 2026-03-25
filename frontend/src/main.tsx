import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import './lib/i18n' // initialise react-i18next before first render
import './index.css'
import App from './App.tsx'
import { NotificationProvider } from './context/NotificationContext'
import { NotificationContainer } from './components/ui/NotificationContainer'

const queryClient = new QueryClient()

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <BrowserRouter>
      <QueryClientProvider client={queryClient}>
        <NotificationProvider>
          <App />
          <NotificationContainer />
        </NotificationProvider>
      </QueryClientProvider>
    </BrowserRouter>
  </StrictMode>,
)
