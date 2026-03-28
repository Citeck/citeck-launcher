import { BrowserRouter, Routes, Route, Navigate } from 'react-router'
import { ErrorBoundary } from './components/ErrorBoundary'
import { useDashboardStore } from './lib/store'
import { getDaemonStatus } from './lib/api'
import { Dashboard } from './pages/Dashboard'
import { AppDetail } from './pages/AppDetail'
import { Logs } from './pages/Logs'
import { Config } from './pages/Config'
import { Volumes } from './pages/Volumes'
import { DaemonLogs } from './pages/DaemonLogs'
import { Welcome } from './pages/Welcome'
import { Wizard } from './pages/Wizard'
import { Secrets } from './pages/Secrets'
import { Diagnostics } from './pages/Diagnostics'
import { TabBar } from './components/TabBar'
import { ToastContainer } from './components/Toast'
import { useEffect, useState } from 'react'

function Layout() {
  const { namespace, fetchData } = useDashboardStore()
  const [isDesktop, setIsDesktop] = useState(false)

  // Fetch daemon status once on mount to detect server/desktop mode
  useEffect(() => {
    getDaemonStatus().then((s) => setIsDesktop(s.desktop)).catch(() => setIsDesktop(false))
  }, [])

  // Fetch namespace status on mount to determine which screen to show
  useEffect(() => {
    fetchData()
  }, [fetchData])

  const hasNamespace = namespace !== null
  // Server mode (desktop=false): always show Dashboard, never Welcome at root
  // Desktop mode (desktop=true or unknown): show Welcome when no namespace
  const showWelcomeAtRoot = isDesktop !== false && !hasNamespace

  return (
    <div className="min-h-screen bg-background flex flex-col">
      <TabBar />
      <main className="flex-1 min-h-0 overflow-auto">
        <Routes>
          {/* Root: Dashboard or Welcome depending on mode and namespace */}
          <Route index element={showWelcomeAtRoot ? <Welcome /> : <Dashboard />} />

          {/* Workspace-level pages (always accessible) */}
          <Route path="/welcome" element={<Welcome />} />
          <Route path="/wizard" element={<Wizard />} />
          <Route path="/secrets" element={<Secrets />} />
          <Route path="/diagnostics" element={<Diagnostics />} />
          <Route path="/daemon-logs" element={<DaemonLogs />} />

          {/* Namespace-level pages (redirect to Welcome if no namespace) */}
          <Route path="/apps/:name" element={hasNamespace ? <AppDetail /> : <Navigate to="/" />} />
          <Route path="/apps/:name/logs" element={hasNamespace ? <Logs /> : <Navigate to="/" />} />
          <Route path="/config" element={hasNamespace ? <Config /> : <Navigate to="/" />} />
          <Route path="/volumes" element={hasNamespace ? <Volumes /> : <Navigate to="/" />} />
        </Routes>
      </main>
    </div>
  )
}

function App() {
  return (
    <ErrorBoundary>
      <BrowserRouter>
        <Layout />
      </BrowserRouter>
      <ToastContainer />
    </ErrorBoundary>
  )
}

export default App
