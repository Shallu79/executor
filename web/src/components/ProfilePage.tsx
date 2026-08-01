import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { authApi, getSessionToken, setSessionToken } from '../api'
import { Button } from './ui'

export default function ProfilePage() {
  const navigate = useNavigate()
  const [email, setEmail] = useState('')
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    authApi
      .me()
      .then((u) => setEmail(u.email))
      .catch(() => setEmail(''))
      .finally(() => setLoading(false))
  }, [])

  function signOut() {
    setSessionToken(null)
    navigate('/')
  }

  return (
    <div className="mx-auto w-full max-w-2xl space-y-6">
      <header>
        <h1 className="text-2xl font-bold text-ink">Profile</h1>
        <p className="mt-1 text-sm text-muted">Your account details.</p>
      </header>

      <div className="flex items-center gap-4 rounded-2xl border border-edge bg-panel p-6">
        <div className="flex h-14 w-14 shrink-0 items-center justify-center rounded-full bg-gradient-to-br from-brand to-brand-2 text-xl font-bold text-white">
          {(email || '?').slice(0, 1).toUpperCase()}
        </div>
        <div className="min-w-0">
          <div className="truncate text-base font-semibold text-ink">
            {loading ? '…' : email || 'Unknown'}
          </div>
          <div className="text-xs text-faint">Signed in{getSessionToken() ? '' : '?'}</div>
        </div>
      </div>

      <div className="flex gap-2">
        <Button variant="ghost" onClick={() => navigate('/keys')}>
          Manage API keys
        </Button>
        <Button variant="danger" onClick={signOut}>
          Sign out
        </Button>
      </div>
    </div>
  )
}
