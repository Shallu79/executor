import type { ReactNode } from 'react'
import { BrowserRouter, Navigate, Route, Routes } from 'react-router-dom'
import { getSessionToken } from './api'
import AccountLayout from './components/AccountLayout'
import AboutPage from './components/AboutPage'
import AuthScreen from './components/AuthScreen'
import IdePage from './components/IdePage'
import KeysSection from './components/KeysSection'
import ProfilePage from './components/ProfilePage'
import SubmissionsSection from './components/SubmissionsSection'

function Protected({ children }: { children: ReactNode }) {
  if (!getSessionToken()) return <Navigate to="/auth" replace />
  return <>{children}</>
}

export default function App() {
  return (
    <BrowserRouter>
      <Routes>
        <Route path="/auth" element={<AuthScreen />} />
        <Route path="/" element={<IdePage />} />
        <Route path="/about" element={<AboutPage />} />
        <Route
          element={
            <Protected>
              <AccountLayout />
            </Protected>
          }
        >
          <Route path="/keys" element={<KeysSection />} />
          <Route path="/submissions" element={<SubmissionsSection />} />
          <Route path="/profile" element={<ProfilePage />} />
        </Route>
        <Route path="*" element={<Navigate to="/" replace />} />
      </Routes>
    </BrowserRouter>
  )
}
