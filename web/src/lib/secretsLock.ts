import { create } from 'zustand'

// Bumped whenever the SecretService transitions to a usable state (master
// password entered → secrets unlocked, or a Kotlin 1.x secret blob imported).
//
// Surfaces that transition to screens whose data depends on secret-gated
// resources. In particular the Welcome Quick Start bundle refs resolve a git
// "LATEST" via the workspace token, which is unavailable while secrets are
// locked: the first quick-starts fetch on a locked start returns the symbolic
// "repo:LATEST", and nothing else re-fetches it after the user unlocks. Welcome
// subscribes to `epoch` and reloads, so the concrete version appears as soon as
// the token becomes usable.
interface SecretsLockState {
  epoch: number
  markUnlocked: () => void
}

export const useSecretsLockStore = create<SecretsLockState>((set) => ({
  epoch: 0,
  markUnlocked: () => set((s) => ({ epoch: s.epoch + 1 })),
}))
