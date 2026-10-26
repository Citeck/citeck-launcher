import { act, render, screen } from '@testing-library/react'
import { useEffect } from 'react'
import { beforeEach, expect, it, vi } from 'vitest'
import { StartupBanners } from './StartupBanners'
import { useSecretsLockStore } from '../lib/secretsLock'

beforeEach(() => useSecretsLockStore.setState({ ready: false, epoch: 0 }))
it('does not mount banner effects or dialogs while awaiting master-password input', () => {
  const mounted = vi.fn()
  const cleanup = vi.fn()
  function Banner() {
    useEffect(() => { mounted(); return cleanup }, [])
    return <div role="alert">Background warning</div>
  }
  render(<StartupBanners><Banner /></StartupBanners>)
  expect(screen.queryByRole('alert')).toBeNull()
  expect(mounted).not.toHaveBeenCalled()
  act(() => useSecretsLockStore.getState().markUnlocked())
  expect(screen.getByRole('alert')).toBeVisible()
  expect(mounted).toHaveBeenCalledOnce()
  act(() => useSecretsLockStore.getState().setReady(false))
  expect(screen.queryByRole('alert')).toBeNull()
  expect(cleanup).toHaveBeenCalledOnce()
})
