import type { ReactNode } from 'react'
import { useSecretsLockStore } from '../lib/secretsLock'

/** Keep attention on master-password entry until fresh namespace data is ready. */
export function StartupBanners({ children }: { children: ReactNode }) {
  const ready = useSecretsLockStore((s) => s.ready)
  return ready ? children : null
}
