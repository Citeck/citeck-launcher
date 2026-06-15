import { create } from 'zustand'

/**
 * Auth gate for the daemon's opt-in API token auth (daemon.yml `api_auth`).
 *
 * When any API call answers 401 with code AUTH_REQUIRED, api.ts flips
 * `required` here and App renders the full-screen AuthRequired prompt. The
 * user either runs `citeck ui` on the host (which opens an authenticated
 * /auth/session link) or pastes the token into the prompt — submitAuthToken
 * performs the same handshake, the daemon sets an HttpOnly session cookie,
 * and the page reloads into an authenticated session.
 */
interface AuthGateState {
  required: boolean
  setRequired: () => void
}

export const useAuthGateStore = create<AuthGateState>((set) => ({
  required: false,
  setRequired: () => set({ required: true }),
}))

/** Called by api.ts on 401 AUTH_REQUIRED responses. */
export function notifyAuthRequired(): void {
  useAuthGateStore.getState().setRequired()
}

/**
 * Exchanges a pasted token for a session cookie via GET /auth/session
 * (outside API_BASE — the handshake is deliberately not under /api so it
 * stays reachable while the API is gated). Resolves true when the daemon
 * accepted the token (cookie is set; caller reloads the page), false when
 * the token was rejected or the request failed.
 */
export async function submitAuthToken(token: string): Promise<boolean> {
  try {
    const res = await fetch(`/auth/session?token=${encodeURIComponent(token)}`)
    return res.ok
  } catch {
    return false
  }
}
