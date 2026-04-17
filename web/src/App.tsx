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
  // Select only the two fields Layout actually consumes. Subscribing to the
  // whole store would force a re-render on every SSE tick (reconnectDelay,
  // lastSeq, etc.), even though Layout's output only depends on `namespace`.
  const namespace = useDashboardStore((s) => s.namespace)
  const fetchData = useDashboardStore((s) => s.fetchData)
  const [isDesktop, setIsDesktop] = useState<boolean | null>(null)

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
  // null = loading (show nothing yet), true = desktop, false = server
  // Desktop mode: show Welcome when no namespace selected
  // Server mode: daemon won't start without namespace (CLI guard), so Dashboard always has data
  const showWelcomeAtRoot = isDesktop === true && !hasNamespace

  return (
    <div className="h-screen bg-background flex flex-col overflow-hidden">
      <TabBar />
      <main className="flex-1 min-h-0 flex flex-col">
        <Routes>
          {/* Root: Welcome (desktop, no namespace) or Dashboard */}
          <Route index element={<Safe>{showWelcomeAtRoot ? <Welcome /> : <Dashboard />}</Safe>} />

          {/* Workspace-level pages (scrollable) */}
          <Route path="/welcome" element={<Scroll><Safe><Welcome /></Safe></Scroll>} />
          <Route path="/wizard" element={<Scroll><Safe><Wizard /></Safe></Scroll>} />
          <Route path="/secrets" element={<Scroll><Safe><Secrets /></Safe></Scroll>} />
          <Route path="/diagnostics" element={<Scroll><Safe><Diagnostics /></Safe></Scroll>} />
          <Route path="/daemon-logs" element={<Scroll><Safe><DaemonLogs /></Safe></Scroll>} />

          {/* Namespace-level pages (scrollable, redirect to Welcome if no namespace) */}
          <Route path="/apps/:name" element={hasNamespace ? <Scroll><Safe><AppDetail /></Safe></Scroll> : <Navigate to="/" />} />
          <Route path="/apps/:name/logs" element={hasNamespace ? <Scroll><Safe><Logs /></Safe></Scroll> : <Navigate to="/" />} />
          <Route path="/config" element={hasNamespace ? <Scroll><Safe><Config /></Safe></Scroll> : <Navigate to="/" />} />
          <Route path="/volumes" element={hasNamespace ? <Scroll><Safe><Volumes /></Safe></Scroll> : <Navigate to="/" />} />
        </Routes>
      </main>
    </div>
  )
}

function Scroll({ children }: { children: React.ReactNode }) {
  return <div className="flex-1 overflow-auto">{children}</div>
}

/** Per-page ErrorBoundary — shows inline error + retry, doesn't kill the whole app. */
function Safe({ children }: { children: React.ReactNode }) {
  return <ErrorBoundary inline>{children}</ErrorBoundary>
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
