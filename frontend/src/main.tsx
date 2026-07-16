import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import './lib/i18n' // initialise react-i18next before first render
import './index.css'
import App from './App.tsx'
import { NotificationProvider } from './context/NotificationContext'
import { NotificationContainer } from './components/ui/NotificationContainer'
import { ConfirmProvider } from './context/ConfirmContext'

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      retry: (failureCount, error) => {
        // Don't retry on auth failures — redirect to login instead.
        if (error && typeof error === 'object' && 'code' in error) {
          const code = (error as { code?: string }).code
          if (code === 'TOKEN_REFRESH_REQUIRED' || code === 'UNAUTHENTICATED') {
            window.location.href = '/login'
            return false
          }
        }
        return failureCount < 2
      },
    },
  },
})

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <BrowserRouter>
      <QueryClientProvider client={queryClient}>
        <NotificationProvider>
          <ConfirmProvider>
            <App />
            <NotificationContainer />
          </ConfirmProvider>
        </NotificationProvider>
      </QueryClientProvider>
    </BrowserRouter>
  </StrictMode>,
)
