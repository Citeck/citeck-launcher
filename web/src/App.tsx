import { BrowserRouter, Routes, Route, Navigate } from 'react-router'
import { ErrorBoundary } from './components/ErrorBoundary'
import { useDashboardStore } from './lib/store'
import { getDaemonStatus } from './lib/api'
import { useI18nStore, type Locale } from './lib/i18n'
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

  // Fetch daemon status once on mount to detect server/desktop mode and locale
  useEffect(() => {
    getDaemonStatus().then((s) => {
      setIsDesktop(s.desktop)
      // Apply server-configured locale if set and user hasn't manually chosen one
      if (s.locale && !localStorage.getItem('citeck-locale')) {
        useI18nStore.getState().setLocale(s.locale as Locale)
      }
    }).catch(() => setIsDesktop(false))
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
    <div className="h-screen bg-background flex flex-col overflow-hidden">
      <TabBar />
      <main className="flex-1 min-h-0 flex flex-col">
        <Routes>
          {/* Root: Dashboard or Welcome depending on mode and namespace */}
          <Route index element={showWelcomeAtRoot ? <Welcome /> : <Dashboard />} />

          {/* Workspace-level pages (scrollable) */}
          <Route path="/welcome" element={<Scroll><Welcome /></Scroll>} />
          <Route path="/wizard" element={<Scroll><Wizard /></Scroll>} />
          <Route path="/secrets" element={<Scroll><Secrets /></Scroll>} />
          <Route path="/diagnostics" element={<Scroll><Diagnostics /></Scroll>} />
          <Route path="/daemon-logs" element={<Scroll><DaemonLogs /></Scroll>} />

          {/* Namespace-level pages (scrollable, redirect to Welcome if no namespace) */}
          <Route path="/apps/:name" element={hasNamespace ? <Scroll><AppDetail /></Scroll> : <Navigate to="/" />} />
          <Route path="/apps/:name/logs" element={hasNamespace ? <Scroll><Logs /></Scroll> : <Navigate to="/" />} />
          <Route path="/config" element={hasNamespace ? <Scroll><Config /></Scroll> : <Navigate to="/" />} />
          <Route path="/volumes" element={hasNamespace ? <Scroll><Volumes /></Scroll> : <Navigate to="/" />} />
        </Routes>
      </main>
    </div>
  )
}

function Scroll({ children }: { children: React.ReactNode }) {
  return <div className="flex-1 overflow-auto">{children}</div>
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
