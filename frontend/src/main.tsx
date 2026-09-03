import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter } from 'react-router-dom'
import { Code, ConnectError } from '@connectrpc/connect'
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
        // Never retry an auth failure: the session is gone, and two more calls
        // will not bring it back. Dropping to the login screen is handled by the
        // transport interceptor, which sees every RPC rather than only the ones
        // React Query happens to own.
        //
        // This compared error.code to the string 'UNAUTHENTICATED'. ConnectError
        // carries a numeric code (Code.Unauthenticated === 16), so the branch
        // never matched and the redirect it guarded never ran.
        if (error instanceof ConnectError && error.code === Code.Unauthenticated) {
          return false
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
