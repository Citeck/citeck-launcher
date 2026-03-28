import { useRef, useCallback, useLayoutEffect, type ReactNode } from 'react'
import { usePanelStore, type BottomPanelTab } from '../lib/panels'
import { useResizeHandle } from '../hooks/useResizeHandle'
import { useTranslation } from '../lib/i18n'
import { X, ChevronDown, ChevronUp } from 'lucide-react'

/** Registry of tab content renderers — populated by the integrating page (Dashboard). */
export type TabRenderer = (tab: BottomPanelTab, active: boolean) => ReactNode

interface BottomPanelProps {
  renderTab: TabRenderer
}

export function BottomPanel({ renderTab }: BottomPanelProps) {
  const { t } = useTranslation()
  const {
    bottomTabs, activeBottomTabId, bottomPanelOpen, bottomPanelHeight,
    closeBottomTab, setActiveBottomTab, setBottomPanelHeight, toggleBottomPanel,
  } = usePanelStore()

  const { handleProps, isResizing } = useResizeHandle({
    currentHeight: bottomPanelHeight,
    onResize: setBottomPanelHeight,
  })

  // Track which tabs have been activated at least once (lazy mount)
  const mountedRef = useRef<Set<string>>(new Set())
  useLayoutEffect(() => {
    if (activeBottomTabId) mountedRef.current.add(activeBottomTabId)
  }, [activeBottomTabId])

  const onCloseTab = useCallback((e: React.MouseEvent, id: string) => {
    e.stopPropagation()
    mountedRef.current.delete(id)
    closeBottomTab(id)
  }, [closeBottomTab])

  if (bottomTabs.length === 0) return null

  return (
    <div className="flex flex-col border-t border-border bg-card shrink-0"
      style={isResizing ? { userSelect: 'none' } : undefined}>
      {/* Drag handle */}
      <div
        {...handleProps}
        className={`h-[5px] cursor-row-resize flex items-center justify-center ${
          isResizing ? 'bg-primary/30' : 'hover:bg-primary/15'
        }`}
      >
        <div className={`w-8 h-[3px] rounded-full ${isResizing ? 'bg-primary/60' : 'bg-border'}`} />
      </div>
      {/* Tab strip */}
      <div className="flex items-center border-b border-border h-8 px-1 gap-0.5 overflow-x-auto shrink-0">
        <button
          type="button"
          className="p-0.5 rounded text-muted-foreground hover:text-foreground hover:bg-muted shrink-0"
          onClick={toggleBottomPanel}
          title={bottomPanelOpen ? t('panel.collapse') : t('panel.expand')}
        >
          {bottomPanelOpen ? <ChevronDown size={14} /> : <ChevronUp size={14} />}
        </button>
        {bottomTabs.map((tab) => (
          <div
            key={tab.id}
            className={`flex items-center gap-1 text-[11px] shrink-0 border-b-2 -mb-px ${
              tab.id === activeBottomTabId
                ? 'border-b-primary text-foreground'
                : 'border-b-transparent text-muted-foreground hover:text-foreground hover:bg-accent'
            }`}
          >
            <button type="button" className="truncate max-w-[140px] px-2 py-0.5"
              onClick={() => setActiveBottomTab(tab.id)}>
              {tab.title}
            </button>
            <button type="button" className="hover:bg-border rounded p-px mr-0.5"
              onClick={(e) => onCloseTab(e, tab.id)} title={t('panel.closeTab')}>
              <X size={12} />
            </button>
          </div>
        ))}
      </div>
      {/* Content area */}
      <div
        className={bottomPanelOpen ? 'overflow-hidden' : 'h-0 overflow-hidden'}
        style={bottomPanelOpen ? { height: bottomPanelHeight } : undefined}
      >
        {bottomTabs.map((tab) => {
          if (!mountedRef.current.has(tab.id)) return null
          const isActive = tab.id === activeBottomTabId
          return (
            <div key={tab.id} className={`h-full ${isActive ? '' : 'hidden'}`}>
              {renderTab(tab, isActive && bottomPanelOpen)}
            </div>
          )
        })}
      </div>
    </div>
  )
}
