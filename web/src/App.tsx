import { BrowserRouter, Routes, Route } from 'react-router'
import { Dashboard } from './pages/Dashboard'
import { AppDetail } from './pages/AppDetail'
import { Logs } from './pages/Logs'
import { Config } from './pages/Config'
import { Volumes } from './pages/Volumes'
import { TabBar } from './components/TabBar'

function Layout() {
  return (
    <div className="min-h-screen bg-background flex flex-col">
      <TabBar />
      <main className="flex-1 min-h-0 overflow-auto">
        <Routes>
          <Route index element={<Dashboard />} />
          <Route path="/apps/:name" element={<AppDetail />} />
          <Route path="/apps/:name/logs" element={<Logs />} />
          <Route path="/config" element={<Config />} />
          <Route path="/volumes" element={<Volumes />} />
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
