import { create } from 'zustand'

export interface Tab {
  id: string
  title: string
  path: string
}

// Namespace-level tab IDs that should be closed when switching to Welcome
const NAMESPACE_TAB_IDS = new Set(['volumes'])

interface TabsState {
  tabs: Tab[]
  activeTabId: string | null
  openTab: (tab: Tab) => void
  closeTab: (id: string) => string | null // returns path to navigate to
  setActiveTab: (id: string) => void
  setHomeTab: (title: string) => void // rename the home tab (Welcome ↔ Dashboard)
  closeNamespaceTabs: () => void // close all namespace-level tabs
}

export const useTabsStore = create<TabsState>((set, get) => ({
  tabs: [{ id: 'home', title: 'Welcome', path: '/' }],
  activeTabId: 'home',

  openTab: (tab) => {
    const { tabs } = get()
    const existing = tabs.find((t) => t.id === tab.id)
    if (existing) {
      set({ activeTabId: tab.id })
    } else {
      set({ tabs: [...tabs, tab], activeTabId: tab.id })
    }
  },

  closeTab: (id) => {
    const { tabs, activeTabId } = get()
    if (id === 'home') return null // can't close home tab
    const newTabs = tabs.filter((t) => t.id !== id)
    if (activeTabId === id) {
      const idx = tabs.findIndex((t) => t.id === id)
      const next = newTabs[Math.min(idx, newTabs.length - 1)]
      set({ tabs: newTabs, activeTabId: next?.id ?? 'home' })
      return next?.path ?? '/'
    }
    set({ tabs: newTabs })
    return null
  },

  setActiveTab: (id) => set({ activeTabId: id }),

  setHomeTab: (title) => {
    const { tabs } = get()
    set({ tabs: tabs.map((t) => (t.id === 'home' ? { ...t, title } : t)) })
  },

  closeNamespaceTabs: () => {
    const { tabs, activeTabId } = get()
    const newTabs = tabs.filter(
      (t) => t.id === 'home' || (!NAMESPACE_TAB_IDS.has(t.id) && !t.path.startsWith('/apps/')),
    )
    const activeStillExists = newTabs.some((t) => t.id === activeTabId)
    set({
      tabs: newTabs,
      activeTabId: activeStillExists ? activeTabId : 'home',
    })
  },
}))
