import { useEffect, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { authApi, getSessionToken, setSessionToken } from '../api'
import { Button } from './ui'

const NAV_ITEMS = [
  { label: 'About', to: '/about', needsAuth: false, icon: <InfoIcon /> },
  { label: 'Profile', to: '/profile', needsAuth: true, icon: <UserIcon /> },
  { label: 'API Keys', to: '/keys', needsAuth: true, icon: <KeyIcon /> },
  { label: 'History', to: '/submissions', needsAuth: true, icon: <HistoryIcon /> },
]

export default function TopBar({
  children,
  center,
}: {
  children?: ReactNode
  center?: ReactNode
}) {
  const navigate = useNavigate()
  const [authed, setAuthed] = useState<boolean>(() => !!getSessionToken())
  const [email, setEmail] = useState('')
  const [menuOpen, setMenuOpen] = useState(false)
  const menuRef = useRef<HTMLDivElement>(null)

  // Re-sync auth state when the route changes (e.g. after login/logout).
  useEffect(() => {
    setAuthed(!!getSessionToken())
    if (getSessionToken()) authApi.me().then((u) => setEmail(u.email)).catch(() => setAuthed(false))
    else setEmail('')
  }, [navigate])

  useEffect(() => {
    const onClick = (e: MouseEvent) => {
      if (menuRef.current && !menuRef.current.contains(e.target as Node)) setMenuOpen(false)
    }
    document.addEventListener('mousedown', onClick)
    return () => document.removeEventListener('mousedown', onClick)
  }, [])

  function signOut() {
    setSessionToken(null)
    setAuthed(false)
    setMenuOpen(false)
    navigate('/')
  }

  function open(item: (typeof NAV_ITEMS)[number]) {
    setMenuOpen(false)
    if (item.needsAuth && !getSessionToken()) {
      navigate('/auth')
      return
    }
    navigate(item.to)
  }

  // "Get API key" takes the user to the API keys page (login first if needed).
  function openKeys() {
    if (!getSessionToken()) {
      navigate('/auth')
      return
    }
    navigate('/keys')
  }

  return (
    <header className="sticky top-0 z-40 flex h-14 items-center justify-between gap-3 border-b border-edge bg-panel/80 px-3 backdrop-blur md:px-6">
      <div className="relative shrink-0" ref={menuRef}>
        <div className="flex items-center gap-2">
          <button
            onClick={() => setMenuOpen((o) => !o)}
            aria-label="Open menu"
            className="flex h-9 w-9 cursor-pointer items-center justify-center rounded-lg border border-edge bg-panel-2 text-ink transition-colors hover:border-muted/40"
          >
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
              {menuOpen ? <path d="M18 6 6 18M6 6l12 12" /> : <path d="M4 6h16M4 12h16M4 18h16" />}
            </svg>
          </button>

          <Link to="/" className="flex items-center gap-1.5">
            <span className="font-pixel text-xs text-brand [text-shadow:2px_2px_0_rgba(0,0,0,0.9)] sm:text-sm">
              MINOS
            </span>
          </Link>
        </div>

        {menuOpen && (
          <div className="absolute left-0 top-full z-50 mt-2 w-56 rounded-xl border border-edge bg-panel p-1.5 shadow-2xl">
            {NAV_ITEMS.map((item) => (
              <button
                key={item.to}
                onClick={() => open(item)}
                className="flex w-full cursor-pointer items-center gap-2.5 rounded-lg px-3 py-2 text-left text-sm text-muted transition-colors hover:bg-panel-2 hover:text-ink"
              >
                <span>{item.icon}</span>
                {item.label}
              </button>
            ))}
            <div className="my-1.5 border-t border-edge" />
            {authed ? (
              <button
                onClick={signOut}
                className="flex w-full cursor-pointer items-center gap-2.5 rounded-lg px-3 py-2 text-left text-sm text-rose-300 transition-colors hover:bg-rose-500/10"
              >
                <span>{SignOutIcon}</span>
                Sign out
              </button>
            ) : (
              <button
                onClick={() => {
                  setMenuOpen(false)
                  navigate('/auth')
                }}
                className="flex w-full cursor-pointer items-center gap-2.5 rounded-lg px-3 py-2 text-left text-sm text-ink transition-colors hover:bg-panel-2"
              >
                <span>{LogInIcon}</span>
                Sign in
              </button>
            )}
          </div>
        )}
      </div>

      {/* Centered slot (e.g. the Run button) */}
      {center && (
        <div className="absolute left-1/2 top-1/2 -translate-x-1/2 -translate-y-1/2">
          {center}
        </div>
      )}

      {/* No overflow-* here: it would clip the absolutely-positioned
          dropdown menus of the child controls. */}
      <div className="flex min-w-0 flex-1 items-center justify-end gap-2">
        {children}

        <Button variant="ghost" className="hidden shrink-0 !py-1.5 text-sm md:inline-flex" onClick={openKeys}>
          <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
            <circle cx="8" cy="15" r="4" />
            <path d="M10.9 12.1 20 3M15 8l3 3M17 6l2 2" />
          </svg>
          Get API key
        </Button>

        {authed ? (
          <div className="hidden shrink-0 sm:block">
            <button
              onClick={() => navigate('/profile')}
              className="flex max-w-[180px] items-center gap-2 rounded-lg border border-edge bg-panel-2 px-3 py-1.5 text-sm text-ink transition-colors hover:border-muted/40"
            >
              <span className="flex h-5 w-5 items-center justify-center rounded-full bg-gradient-to-br from-brand to-brand-2 text-[10px] font-bold text-white">
                {(email || '?').slice(0, 1).toUpperCase()}
              </span>
              <span className="hidden truncate md:block">{email || 'Account'}</span>
            </button>
          </div>
        ) : (
          <Button onClick={() => navigate('/auth')} className="shrink-0 !py-1.5 text-sm">
            Log in
          </Button>
        )}
      </div>
    </header>
  )
}

function InfoIcon() {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
      <circle cx="12" cy="12" r="9" />
      <path d="M12 8h.01M12 11v5" />
    </svg>
  )
}

function UserIcon() {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
      <circle cx="12" cy="8" r="4" />
      <path d="M4 21c0-4 3.6-6 8-6s8 2 8 6" />
    </svg>
  )
}

function KeyIcon() {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
      <circle cx="8" cy="15" r="4" />
      <path d="M10.9 12.1 20 3M15 8l3 3M17 6l2 2" />
    </svg>
  )
}

function HistoryIcon() {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
      <path d="M3 12a9 9 0 1 0 3-6.7L3 8" />
      <path d="M3 4v4h4M12 7v5l3 2" />
    </svg>
  )
}

const SignOutIcon = (
  <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
    <path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4M16 17l5-5-5-5M21 12H9" />
  </svg>
)

const LogInIcon = (
  <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
    <path d="M15 3h4a2 2 0 0 1 2 2v14a2 2 0 0 1-2 2h-4M10 17l5-5-5-5M15 12H3" />
  </svg>
)
