import { BrowserRouter, Routes, Route, Link, useLocation, useNavigate } from 'react-router'
import { Dashboard } from './pages/Dashboard'
import { AppDetail } from './pages/AppDetail'
import { Logs } from './pages/Logs'
import { Config } from './pages/Config'
import { TabBar } from './components/TabBar'
import { useTabsStore } from './lib/tabs'
import { LayoutGrid, Settings } from 'lucide-react'

function NavLink({ to, tabId, tabTitle, icon: Icon, children }: {
  to: string; tabId?: string; tabTitle?: string; icon: React.ElementType; children: React.ReactNode
}) {
  const location = useLocation()
  const navigate = useNavigate()
  const openTab = useTabsStore((s) => s.openTab)
  const active = location.pathname === to

  function handleClick(e: React.MouseEvent) {
    e.preventDefault()
    if (tabId) {
      openTab({ id: tabId, title: tabTitle ?? String(children), path: to })
    }
    navigate(to)
  }

  return (
    <a
      href={to}
      onClick={handleClick}
      className={`flex items-center gap-1.5 px-2 py-1 text-xs rounded ${
        active ? 'bg-muted text-foreground font-medium' : 'text-foreground/70 hover:text-foreground hover:bg-muted/50'
      }`}
    >
      <Icon size={14} />
      {children}
    </a>
  )
}

function Layout() {
  return (
    <div className="min-h-screen bg-background flex">
      <aside className="w-40 shrink-0 border-r border-border bg-card flex flex-col">
        <div className="px-3 py-2 border-b border-border">
          <Link to="/" className="text-sm font-semibold text-foreground">Citeck</Link>
        </div>
        <nav className="flex flex-col gap-0.5 p-1.5">
          <NavLink to="/" tabId="dashboard" tabTitle="Dashboard" icon={LayoutGrid}>Dashboard</NavLink>
          <NavLink to="/config" tabId="config" tabTitle="Config" icon={Settings}>Config</NavLink>
        </nav>
      </aside>
      <div className="flex-1 min-w-0 flex flex-col">
        <TabBar />
        <main className="flex-1 min-h-0 overflow-auto">
          <Routes>
            <Route index element={<Dashboard />} />
            <Route path="/apps/:name" element={<AppDetail />} />
            <Route path="/apps/:name/logs" element={<Logs />} />
            <Route path="/config" element={<Config />} />
          </Routes>
        </main>
      </div>
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
