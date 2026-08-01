import { useState } from 'react'
import type { FormEvent } from 'react'
import { useNavigate } from 'react-router-dom'
import { authApi, setSessionToken, setNewKey } from '../api'
import { Button, Field, Spinner, inputCls } from './ui'

type Mode = 'signin' | 'signup'

export default function AuthScreen() {
  const navigate = useNavigate()
  const [mode, setMode] = useState<Mode>('signin')
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError('')
    setNotice('')
    try {
      if (mode === 'signup') {
        const su = await authApi.signup({ email, password })
        setNotice('Account created. Signing you in…')
        const s = await authApi.signin({ email, password })
        setSessionToken(s.token)
        setNewKey(su.api_key)
      } else {
        const s = await authApi.signin({ email, password })
        setSessionToken(s.token)
      }
      navigate('/', { replace: true })
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Something went wrong')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="flex min-h-screen items-center justify-center px-4">
      <div className="w-full max-w-sm">
        <div className="mb-8 text-center">
          <div className="mx-auto mb-3 flex h-14 w-14 items-center justify-center rounded-2xl bg-linear-to-br from-brand to-brand-2 text-2xl font-black text-white shadow-xl shadow-green-950/50">
            M
          </div>
          <h1 className="text-2xl font-bold text-ink">Minos</h1>
          <p className="mt-1 text-sm text-muted">Secure, multi-language code execution</p>
        </div>

        <div className="rounded-2xl border border-edge bg-panel p-6 shadow-2xl">
          <div className="mb-5 grid grid-cols-2 gap-1 rounded-lg bg-surface p-1">
            {(['signin', 'signup'] as const).map((m) => (
              <button
                key={m}
                onClick={() => {
                  setMode(m)
                  setError('')
                  setNotice('')
                }}
                className={`rounded-md py-1.5 text-sm font-medium transition-colors cursor-pointer ${
                  mode === m ? 'bg-panel-2 text-ink shadow' : 'text-muted hover:text-ink'
                }`}
              >
                {m === 'signin' ? 'Sign in' : 'Sign up'}
              </button>
            ))}
          </div>

          <form onSubmit={submit} className="space-y-4">
            <Field label="Email">
              <input
                className={inputCls}
                type="email"
                required
                autoComplete="email"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                placeholder="you@example.com"
              />
            </Field>
            <Field
              label="Password"
              hint={mode === 'signup' ? 'At least 8 characters' : undefined}
            >
              <input
                className={inputCls}
                type="password"
                required
                autoComplete={mode === 'signup' ? 'new-password' : 'current-password'}
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                placeholder="••••••••"
              />
            </Field>

            {error && (
              <p className="rounded-lg border border-rose-500/30 bg-rose-500/10 px-3 py-2 text-xs text-rose-300">
                {error}
              </p>
            )}
            {notice && (
              <p className="rounded-lg border border-emerald-500/30 bg-emerald-500/10 px-3 py-2 text-xs text-emerald-300">
                {notice}
              </p>
            )}

            <Button type="submit" disabled={busy} className="w-full">
              {busy ? <Spinner /> : null}
              {mode === 'signin' ? 'Sign in' : 'Create account'}
            </Button>
          </form>
        </div>
      </div>
    </div>
  )
}
