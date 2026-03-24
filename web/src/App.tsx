import { BrowserRouter, Routes, Route, Link, useLocation } from 'react-router'
import { Dashboard } from './pages/Dashboard'
import { AppDetail } from './pages/AppDetail'
import { Logs } from './pages/Logs'
import { Config } from './pages/Config'

function NavLink({ to, children }: { to: string; children: React.ReactNode }) {
  const location = useLocation()
  const active = location.pathname === to
  return (
    <Link
      to={to}
      className={`text-sm px-2 py-1 rounded ${
        active ? 'bg-muted text-foreground' : 'text-muted-foreground hover:text-foreground'
      }`}
    >
      {children}
    </Link>
  )
}

function Layout() {
  return (
    <div className="min-h-screen bg-background">
      <header className="border-b border-border bg-card">
        <div className="mx-auto max-w-7xl px-6 py-3 flex items-center justify-between">
          <div className="flex items-center gap-4">
            <Link to="/" className="text-lg font-semibold">
              Citeck
            </Link>
            <nav className="flex items-center gap-1">
              <NavLink to="/">Dashboard</NavLink>
              <NavLink to="/config">Config</NavLink>
            </nav>
          </div>
        </div>
      </header>
      <main className="mx-auto max-w-7xl px-6 py-6">
        <Routes>
          <Route index element={<Dashboard />} />
          <Route path="/apps/:name" element={<AppDetail />} />
          <Route path="/apps/:name/logs" element={<Logs />} />
          <Route path="/config" element={<Config />} />
        </Routes>
      </main>
    </div>
  )
}

function App() {
  return (
    <BrowserRouter>
      <Layout />
    </BrowserRouter>
  )
}

export default App
