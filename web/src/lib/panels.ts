import { create } from 'zustand'

export interface BottomPanelTab {
  id: string           // "logs:eapps" | "ns-config" | "daemon-logs" | "app-config:emodel"
  type: 'logs' | 'ns-config' | 'daemon-logs' | 'app-config' | 'restart-events'
  title: string
  appName?: string     // for logs and app-config types
}

interface PanelState {
  drawerAppName: string | null
  bottomTabs: BottomPanelTab[]
  activeBottomTabId: string | null
  bottomPanelOpen: boolean
  bottomPanelHeight: number

  openDrawer: (appName: string) => void
  closeDrawer: () => void
  openBottomTab: (tab: BottomPanelTab) => void
  closeBottomTab: (id: string) => void
  setActiveBottomTab: (id: string) => void
  setBottomPanelHeight: (h: number) => void
  toggleBottomPanel: () => void
  resetPanels: () => void
}

const STORAGE_KEY = 'citeck-bp-height'
const DEFAULT_HEIGHT = 250
const MIN_HEIGHT = 120

function loadHeight(): number {
  try {
    const v = localStorage.getItem(STORAGE_KEY)
    if (v) {
      const n = parseInt(v, 10)
      if (n >= MIN_HEIGHT) return n
    }
  } catch { /* ignore */ }
  return DEFAULT_HEIGHT
}

export const usePanelStore = create<PanelState>((set, get) => ({
  drawerAppName: null,
  bottomTabs: [],
  activeBottomTabId: null,
  bottomPanelOpen: false,
  bottomPanelHeight: loadHeight(),

  openDrawer: (appName) => {
    set({ drawerAppName: appName })
  },

  closeDrawer: () => {
    set({ drawerAppName: null })
  },

  openBottomTab: (tab) => {
    const { bottomTabs } = get()
    const existing = bottomTabs.find((t) => t.id === tab.id)
    if (existing) {
      set({ activeBottomTabId: tab.id, bottomPanelOpen: true })
    } else {
      set({
        bottomTabs: [...bottomTabs, tab],
        activeBottomTabId: tab.id,
        bottomPanelOpen: true,
      })
    }
  },

  closeBottomTab: (id) => {
    const { bottomTabs, activeBottomTabId } = get()
    const newTabs = bottomTabs.filter((t) => t.id !== id)
    if (newTabs.length === 0) {
      set({ bottomTabs: [], activeBottomTabId: null, bottomPanelOpen: false })
      return
    }
    if (activeBottomTabId === id) {
      const idx = bottomTabs.findIndex((t) => t.id === id)
      const next = newTabs[Math.min(idx, newTabs.length - 1)]
      set({ bottomTabs: newTabs, activeBottomTabId: next.id })
    } else {
      set({ bottomTabs: newTabs })
    }
  },

  setActiveBottomTab: (id) => {
    set({ activeBottomTabId: id, bottomPanelOpen: true })
  },

  setBottomPanelHeight: (h) => {
    const maxH = Math.floor(window.innerHeight * 0.7)
    const clamped = Math.max(MIN_HEIGHT, Math.min(h, maxH))
    set({ bottomPanelHeight: clamped })
    try { localStorage.setItem(STORAGE_KEY, String(clamped)) } catch { /* ignore */ }
  },

  toggleBottomPanel: () => {
    set((s) => ({ bottomPanelOpen: !s.bottomPanelOpen }))
  },

  resetPanels: () => {
    set({
      drawerAppName: null,
      bottomTabs: [],
      activeBottomTabId: null,
      bottomPanelOpen: false,
    })
  },
}))
