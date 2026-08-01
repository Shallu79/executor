import { Outlet } from 'react-router-dom'
import TopBar from './TopBar'

export default function AccountLayout() {
  return (
    <div className="flex min-h-screen flex-col">
      <TopBar />
      <main className="mx-auto w-full max-w-5xl flex-1 px-4 py-8 md:px-8">
        <Outlet />
      </main>
    </div>
  )
}
