import { create } from 'zustand'

export interface Tab {
  id: string
  title: string
  path: string
}

interface TabsState {
  tabs: Tab[]
  activeTabId: string | null
  openTab: (tab: Tab) => void
  closeTab: (id: string) => string | null // returns path to navigate to
  setActiveTab: (id: string) => void
}

export const useTabsStore = create<TabsState>((set, get) => ({
  tabs: [{ id: 'dashboard', title: 'Dashboard', path: '/' }],
  activeTabId: 'dashboard',

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
    if (id === 'dashboard') return null // can't close dashboard
    const newTabs = tabs.filter((t) => t.id !== id)
    if (activeTabId === id) {
      const idx = tabs.findIndex((t) => t.id === id)
      const next = newTabs[Math.min(idx, newTabs.length - 1)]
      set({ tabs: newTabs, activeTabId: next?.id ?? 'dashboard' })
      return next?.path ?? '/'
    }
    set({ tabs: newTabs })
    return null
  },

  setActiveTab: (id) => set({ activeTabId: id }),
}))
