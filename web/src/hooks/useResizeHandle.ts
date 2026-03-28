import { useCallback, useRef, useState } from 'react'

interface UseResizeHandleOptions {
  currentHeight: number
  onResize: (height: number) => void
  minHeight?: number
  maxHeightFraction?: number
}

export function useResizeHandle({
  currentHeight,
  onResize,
  minHeight = 120,
  maxHeightFraction = 0.7,
}: UseResizeHandleOptions) {
  const [isResizing, setIsResizing] = useState(false)
  const startY = useRef(0)
  const startHeight = useRef(0)

  const onPointerDown = useCallback(
    (e: React.PointerEvent) => {
      e.preventDefault()
      const el = e.currentTarget as HTMLElement
      el.setPointerCapture(e.pointerId)
      startY.current = e.clientY
      startHeight.current = currentHeight
      setIsResizing(true)

      const onPointerMove = (ev: PointerEvent) => {
        const delta = startY.current - ev.clientY
        const maxH = Math.floor(window.innerHeight * maxHeightFraction)
        const newH = Math.max(minHeight, Math.min(startHeight.current + delta, maxH))
        onResize(newH)
      }

      const cleanup = () => {
        el.removeEventListener('pointermove', onPointerMove)
        el.removeEventListener('pointerup', cleanup)
        el.removeEventListener('pointercancel', cleanup)
        setIsResizing(false)
      }

      el.addEventListener('pointermove', onPointerMove)
      el.addEventListener('pointerup', cleanup)
      el.addEventListener('pointercancel', cleanup)
    },
    [currentHeight, onResize, minHeight, maxHeightFraction],
  )

  const handleProps = {
    onPointerDown,
    style: { touchAction: 'none' as const },
  }

  return { handleProps, isResizing }
}
