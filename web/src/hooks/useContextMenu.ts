import { useState, useCallback } from 'react'
import type { ContextMenuItem } from '../components/ContextMenu'

interface ContextMenuState {
  items: ContextMenuItem[]
  position: { x: number; y: number }
}

export function useContextMenu() {
  const [contextMenu, setContextMenu] = useState<ContextMenuState | null>(null)

  const showContextMenu = useCallback(
    (e: React.MouseEvent, items: ContextMenuItem[]) => {
      e.preventDefault()
      setContextMenu({
        items,
        position: { x: e.clientX, y: e.clientY },
      })
    },
    [],
  )

  const hideContextMenu = useCallback(() => {
    setContextMenu(null)
  }, [])

  return { contextMenu, showContextMenu, hideContextMenu }
}
