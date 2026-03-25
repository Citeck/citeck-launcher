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
      className={`block px-2 py-0.5 text-xs rounded ${
        active ? 'bg-muted text-foreground' : 'text-muted-foreground hover:text-foreground'
      }`}
    >
      {children}
    </Link>
  )
}

function Layout() {
  return (
    <div className="min-h-screen bg-background flex">
      {/* Sidebar */}
      <aside className="w-44 shrink-0 border-r border-border bg-card flex flex-col">
        <div className="px-3 py-2 border-b border-border">
          <Link to="/" className="text-sm font-semibold text-foreground">Citeck</Link>
        </div>
        <nav className="flex flex-col gap-0.5 p-2">
          <NavLink to="/">Dashboard</NavLink>
          <NavLink to="/config">Config</NavLink>
        </nav>
      </aside>
      {/* Main content */}
      <main className="flex-1 min-w-0 p-3 overflow-auto">
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
