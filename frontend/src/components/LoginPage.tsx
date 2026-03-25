import { useState, useEffect } from 'react'
import { useTranslation } from 'react-i18next'
import { Activity, Lock, User, Loader2, ShieldCheck } from 'lucide-react'
import { fetchAuthProviders, loginWithLDAP } from '../lib/api'

export function LoginPage() {
  const { t } = useTranslation()
  const [providers, setProviders] = useState<string[]>([])
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)

  useEffect(() => {
    fetchAuthProviders().then(setProviders)
  }, [])

  const hasOIDC = providers.includes('oidc')
  const hasLDAP = providers.includes('ldap')

  async function handleLDAPSubmit(e: React.FormEvent) {
    e.preventDefault()
    setError('')
    setLoading(true)
    try {
      await loginWithLDAP(username, password)
      window.location.reload()
    } catch {
      setError(t('auth.invalidCredentials'))
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="min-h-screen flex">
      {/* Left branding panel */}
      <div className="w-[40%] bg-primary flex flex-col items-center justify-center px-12 relative overflow-hidden">
        {/* ECG pattern overlay */}
        <div className="absolute inset-0 opacity-10">
          <svg className="w-full h-full" viewBox="0 0 400 400" preserveAspectRatio="xMidYMid slice">
            <defs>
              <pattern id="ecg-pattern" x="0" y="0" width="100" height="50" patternUnits="userSpaceOnUse">
                <polyline
                  points="0,25 20,25 25,10 30,40 35,15 40,35 45,25 100,25"
                  fill="none"
                  stroke="white"
                  strokeWidth="1.5"
                />
              </pattern>
            </defs>
            <rect width="100%" height="100%" fill="url(#ecg-pattern)" />
          </svg>
        </div>

        <div className="relative z-10 text-center">
          <div className="w-16 h-16 bg-primary-foreground/20 rounded-2xl flex items-center justify-center mx-auto mb-6">
            <Activity className="w-8 h-8 text-primary-foreground" />
          </div>
          <h1 className="text-3xl font-bold text-primary-foreground mb-3">ECG Hub</h1>
          <p className="text-primary-foreground/70 text-sm leading-relaxed max-w-xs">
            Gestion centralisée des électrocardiogrammes
          </p>
          <div className="mt-8 flex items-center justify-center gap-2 text-primary-foreground/50 text-xs">
            <ShieldCheck className="w-3.5 h-3.5" />
            <span>IHU Liryc — Bordeaux</span>
          </div>
        </div>
      </div>

      {/* Right form panel */}
      <div className="flex-1 bg-card flex items-center justify-center px-16">
        <div className="w-full max-w-sm">
          <h2 className="text-xl font-semibold text-foreground mb-1">{t('auth.login')}</h2>
          <p className="text-sm text-muted-foreground mb-8">
            Accédez à votre espace de gestion ECG
          </p>

          {providers.length === 0 && (
            <div className="flex items-center justify-center py-8">
              <Loader2 className="w-5 h-5 animate-spin text-muted-foreground" />
            </div>
          )}

          {hasOIDC && (
            <a
              href="/api/v1/auth/oidc/login"
              className="w-full flex items-center justify-center gap-2 bg-primary text-primary-foreground font-medium text-sm py-2.5 px-4 rounded-lg hover:bg-primary/90 transition-colors"
            >
              <ShieldCheck className="w-4 h-4" />
              {t('auth.ssoLogin')}
            </a>
          )}

          {hasOIDC && hasLDAP && (
            <div className="flex items-center gap-3 my-6">
              <div className="flex-1 h-px bg-border" />
              <span className="text-xs text-muted-foreground">ou</span>
              <div className="flex-1 h-px bg-border" />
            </div>
          )}

          {hasLDAP && (
            <form onSubmit={handleLDAPSubmit} className="space-y-4">
              <div>
                <label className="text-xs font-medium text-muted-foreground mb-1.5 block">
                  {t('auth.username')}
                </label>
                <div className="relative">
                  <User className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-muted-foreground" />
                  <input
                    autoFocus={!hasOIDC}
                    type="text"
                    value={username}
                    onChange={(e) => setUsername(e.target.value)}
                    placeholder="nom.utilisateur"
                    className="w-full pl-10 pr-4 py-2.5 text-sm bg-background border border-input rounded-lg focus:outline-none focus:ring-2 focus:ring-ring/30 focus:border-primary transition-all placeholder:text-muted-foreground/50"
                    required
                  />
                </div>
              </div>

              <div>
                <label className="text-xs font-medium text-muted-foreground mb-1.5 block">
                  {t('auth.password')}
                </label>
                <div className="relative">
                  <Lock className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-muted-foreground" />
                  <input
                    type="password"
                    value={password}
                    onChange={(e) => setPassword(e.target.value)}
                    placeholder="••••••••"
                    className="w-full pl-10 pr-4 py-2.5 text-sm bg-background border border-input rounded-lg focus:outline-none focus:ring-2 focus:ring-ring/30 focus:border-primary transition-all placeholder:text-muted-foreground/50"
                    required
                  />
                </div>
              </div>

              {error && (
                <div className="bg-destructive/5 border border-destructive/20 rounded-lg px-3 py-2">
                  <p className="text-xs text-destructive">{error}</p>
                </div>
              )}

              <button
                type="submit"
                disabled={loading}
                className="w-full bg-foreground text-card font-medium text-sm py-2.5 px-4 rounded-lg hover:bg-foreground/90 transition-colors disabled:opacity-50 flex items-center justify-center gap-2"
              >
                {loading && <Loader2 className="w-4 h-4 animate-spin" />}
                {loading ? t('auth.signingIn') : t('auth.login')}
              </button>
            </form>
          )}

          <p className="text-[10px] text-muted-foreground text-center mt-8">
            IHU Liryc — ECG Hub
          </p>
        </div>
      </div>
    </div>
  )
}
