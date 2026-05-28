import { BrowserRouter, Routes, Route, Navigate, useLocation } from 'react-router'
import { ErrorBoundary } from './components/ErrorBoundary'
import { useDashboardStore } from './lib/store'
import { useDaemonStatusStore, useIsDesktop } from './lib/daemonStatus'
import { useI18nStore, type Locale } from './lib/i18n'
import { primeDesktopModeCache, detectInstalledButStopped } from './lib/desktop'
import { Dashboard } from './pages/Dashboard'
import { AppDetail } from './pages/AppDetail'
import { Logs } from './pages/Logs'
import { Config } from './pages/Config'
import { Volumes } from './pages/Volumes'
import { DaemonLogs } from './pages/DaemonLogs'
import { Welcome } from './pages/Welcome'
import { Secrets } from './pages/Secrets'
import { Diagnostics } from './pages/Diagnostics'
import { Licenses } from './pages/Licenses'
import { DockerNotAvailable } from './pages/DockerNotAvailable'
import { WindowLogs } from './pages/WindowLogs'
import { WindowEditor } from './pages/WindowEditor'
import { TabBar } from './components/TabBar'
import { ToastContainer } from './components/Toast'
import { ErrorModalHost } from './components/ErrorModal'
import { LoadingOverlayHost } from './components/LoadingOverlay'
import { SecretsUnlockGuard } from './components/SecretsUnlockGuard'
import { useEffect } from 'react'

function MainLayout() {
  // Select only the two fields Layout actually consumes. Subscribing to the
  // whole store would force a re-render on every SSE tick (reconnectDelay,
  // lastSeq, etc.), even though Layout's output only depends on `namespace`.
  const namespace = useDashboardStore((s) => s.namespace)
  const health = useDashboardStore((s) => s.health)
  const fetchData = useDashboardStore((s) => s.fetchData)
  const isDesktop = useIsDesktop()

  useEffect(() => {
    primeDesktopModeCache()
    useDaemonStatusStore.getState().fetch().then((s) => {
      if (s?.locale && !localStorage.getItem('citeck-locale')) {
        useI18nStore.getState().setLocale(s.locale as Locale)
      }
    })
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

  // Kotlin parity: full-screen DockerNotAvailable takes over the layout when
  // the daemon's health check reports docker as unreachable.
  const dockerCheck = health?.checks.find((c) => c.name === 'docker')
  if (dockerCheck?.status === 'error') {
    return (
      <DockerNotAvailable
        error={dockerCheck.message}
        installedButStopped={detectInstalledButStopped(dockerCheck.message)}
        onRetry={fetchData}
      />
    )
  }

  return (
    <div className="h-screen bg-background flex flex-col overflow-hidden">
      {/* Master-password prompt — runs once on app mount before any namespace
          start, so private-registry pulls (Harbor, nexus, etc.) have access
          to creds from the unlocked SecretService. */}
      <SecretsUnlockGuard />
      <TabBar />
      <main className="flex-1 min-h-0 flex flex-col">
        <Routes>
          {/* Root: Welcome (desktop, no namespace) or Dashboard */}
          <Route index element={<Safe>{showWelcomeAtRoot ? <Welcome /> : <Dashboard />}</Safe>} />

          {/* Workspace-level pages (scrollable) */}
          <Route path="/welcome" element={<Scroll><Safe><Welcome /></Safe></Scroll>} />
          <Route path="/secrets" element={<Scroll><Safe><Secrets /></Safe></Scroll>} />
          <Route path="/diagnostics" element={<Scroll><Safe><Diagnostics /></Safe></Scroll>} />
          <Route path="/licenses" element={<Scroll><Safe><Licenses /></Safe></Scroll>} />
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

/**
 * Top-level router: paths under `/window/*` get a chromeless layout (used by
 * the desktop multi-window mode); everything else uses the main app shell.
 * The window path uses the same routing tree because Wails loads it into a
 * new WebviewWindow with full SPA bundle — splitting bundles would just bloat
 * startup time.
 */
function Layout() {
  const location = useLocation()
  if (location.pathname.startsWith('/window/')) {
    return (
      <Routes>
        <Route path="/window/logs/:name" element={<Safe><WindowLogs /></Safe>} />
        <Route path="/window/daemon-logs" element={<Safe><WindowLogs /></Safe>} />
        <Route path="/window/editor/:name" element={<Safe><WindowEditor /></Safe>} />
      </Routes>
    )
  }
  return <MainLayout />
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
      <ErrorModalHost />
      <LoadingOverlayHost />
    </ErrorBoundary>
  )
}

export default App
