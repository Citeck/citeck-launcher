import { useNavigate, useLocation } from 'react-router'
import { useTabsStore } from '../lib/tabs'
import { usePanelStore } from '../lib/panels'
import { X, Settings, Sun, Moon } from 'lucide-react'
import { useEffect, useState } from 'react'

export function TabBar() {
  const { tabs, activeTabId, setActiveTab, closeTab } = useTabsStore()
  const openBottomTab = usePanelStore((s) => s.openBottomTab)
  const navigate = useNavigate()
  const location = useLocation()

  // Sync active tab with current location
  useEffect(() => {
    const match = tabs.find((t) => t.path === location.pathname)
    if (match && match.id !== activeTabId) {
      setActiveTab(match.id)
    }
  }, [location.pathname, tabs, activeTabId, setActiveTab])

  return (
    <div className="flex items-center border-b border-border bg-card shrink-0">
      <div className="flex items-center overflow-x-auto flex-1 min-w-0">
        {tabs.map((tab) => {
          const isActive = tab.id === activeTabId
          return (
            <div
              key={tab.id}
              className={`group flex items-center gap-1 px-3 py-1.5 text-xs border-r border-border cursor-pointer shrink-0 select-none ${
                isActive
                  ? 'bg-background text-foreground border-b-2 border-b-primary -mb-px'
                  : 'text-muted-foreground hover:text-foreground hover:bg-accent'
              }`}
              onClick={() => {
                setActiveTab(tab.id)
                navigate(tab.path)
              }}
            >
              <span className="truncate max-w-[140px]">{tab.title}</span>
              {tab.id !== 'home' && (
                <button
                  type="button"
                  className="ml-0.5 p-0.5 rounded hover:bg-muted opacity-0 group-hover:opacity-100 transition-opacity"
                  onClick={(e) => {
                    e.stopPropagation()
                    const navTo = closeTab(tab.id)
                    if (navTo !== null) navigate(navTo)
                  }}
                >
                  <X size={10} />
                </button>
              )}
            </div>
          )
        })}
      </div>
      {/* Right-side buttons */}
      <div className="flex items-center border-l border-border shrink-0">
        <ThemeToggle />
        <button
          type="button"
          className="p-1.5 text-muted-foreground hover:text-foreground hover:bg-muted"
          title="Settings"
          onClick={() => {
            if (location.pathname === '/') {
              openBottomTab({ id: 'ns-config', type: 'ns-config', title: 'Config: ns.yml' })
            } else {
              navigate('/config')
            }
          }}
        >
          <Settings size={14} />
        </button>
      </div>
    </div>
  )
}

function ThemeToggle() {
  const [isDark, setIsDark] = useState(() => {
    try {
      const stored = localStorage.getItem('theme')
      if (stored) return stored === 'dark'
      return !window.matchMedia?.('(prefers-color-scheme: light)').matches
    } catch {
      return true // default to dark
    }
  })

  useEffect(() => {
    document.documentElement.setAttribute('data-theme', isDark ? 'dark' : 'light')
    localStorage.setItem('theme', isDark ? 'dark' : 'light')
  }, [isDark])

  return (
    <button
      type="button"
      className="p-1.5 text-muted-foreground hover:text-foreground hover:bg-muted"
      title={isDark ? 'Switch to light theme' : 'Switch to dark theme'}
      onClick={() => setIsDark((d) => !d)}
    >
      {isDark ? <Sun size={14} /> : <Moon size={14} />}
    </button>
  )
}
