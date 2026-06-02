import type { ReactNode } from 'react'
import { Loader2 } from 'lucide-react'

/**
 * Button label that keeps its width while loading. The label stays in the DOM
 * (occupying its normal space) but turns invisible, and a centered spinner is
 * overlaid on top. This avoids the layout jump caused by swapping the text for
 * "Working…" or appending "…", which changes the button's width.
 */
export function LoadingLabel({ loading, children }: { loading: boolean; children: ReactNode }) {
  return (
    <span className="relative inline-flex items-center justify-center">
      <span className={loading ? 'invisible' : ''}>{children}</span>
      {loading && <Loader2 className="absolute inset-0 m-auto h-4 w-4 animate-spin" />}
    </span>
  )
}
