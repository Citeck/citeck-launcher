import { useNavigate, useLocation } from 'react-router'
import { useTabsStore } from '../lib/tabs'
import { X, Settings } from 'lucide-react'
import { useEffect } from 'react'

export function TabBar() {
  const { tabs, activeTabId, setActiveTab, closeTab, openTab } = useTabsStore()
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
                  ? 'bg-background text-foreground'
                  : 'text-muted-foreground hover:text-foreground hover:bg-muted/30'
              }`}
              onClick={() => {
                setActiveTab(tab.id)
                navigate(tab.path)
              }}
            >
              <span className="truncate max-w-[140px]">{tab.title}</span>
              {tab.id !== 'dashboard' && (
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
      {/* Config button on the right */}
      <button
        type="button"
        className="p-1.5 text-muted-foreground hover:text-foreground hover:bg-muted border-l border-border shrink-0"
        title="Settings"
        onClick={() => {
          openTab({ id: 'config', title: 'Config', path: '/config' })
          navigate('/config')
        }}
      >
        <Settings size={14} />
      </button>
    </div>
  )
}
