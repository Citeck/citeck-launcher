import { Dashboard } from './pages/Dashboard'

function App() {
  return (
    <div className="min-h-screen bg-background">
      {/* Top bar */}
      <header className="border-b border-border bg-card">
        <div className="mx-auto max-w-7xl px-6 py-3 flex items-center justify-between">
          <div className="flex items-center gap-2">
            <span className="text-lg font-semibold">Citeck</span>
            <span className="text-xs text-muted-foreground bg-muted px-1.5 py-0.5 rounded">
              Dashboard
            </span>
          </div>
        </div>
      </header>

      {/* Main content */}
      <main className="mx-auto max-w-7xl px-6 py-6">
        <Dashboard />
      </main>
    </div>
  )
}

export default App
