import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { getNamespace, postGitSkipPull } from './api'
import { useI18nStore } from './i18n'
import { connectEvents } from './websocket'

/**
 * The web UI's half of the locale contract.
 *
 * The daemon builds every sentence it composes itself — the dependency
 * migration preflights, the edit-gate refusals, the long-operation refusals —
 * as a locale key plus arguments, and renders it at the request boundary. A
 * request that states no language is answered in daemon.yml's, which on a
 * desktop is not this window's. Nothing FAILS when the header is missing: the
 * screen just comes back in another language, which is precisely why it needs
 * a test rather than a code review.
 */

function jsonResponse(body: unknown): Response {
  return {
    ok: true,
    status: 200,
    statusText: 'OK',
    json: async () => body,
    text: async () => JSON.stringify(body),
  } as unknown as Response
}

const fetchMock = vi.fn()

function lastHeaders(): Record<string, string> {
  const call = fetchMock.mock.calls.at(-1)
  expect(call).toBeDefined()
  return (call![1] as RequestInit).headers as Record<string, string>
}

describe('every daemon request states the UI locale', () => {
  beforeEach(() => {
    vi.stubGlobal('fetch', fetchMock)
    fetchMock.mockReset()
    useI18nStore.setState({ locale: 'en' })
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    useI18nStore.setState({ locale: 'en' })
  })

  it('GET carries X-Citeck-Locale', async () => {
    useI18nStore.setState({ locale: 'ru' })
    fetchMock.mockResolvedValueOnce(jsonResponse({ id: 'ns1' }))
    await getNamespace()
    expect(lastHeaders()['X-Citeck-Locale']).toBe('ru')
  })

  it('a mutating request carries it too, beside the CSRF header', async () => {
    useI18nStore.setState({ locale: 'de' })
    fetchMock.mockResolvedValueOnce(jsonResponse({ success: true, message: '' }))
    await postGitSkipPull('gitlab.example.com', 60)
    const headers = lastHeaders()
    expect(headers['X-Citeck-Locale']).toBe('de')
    expect(headers['X-Citeck-CSRF']).toBe('1')
  })

  it('follows a language change on the NEXT request, not on the next mount', async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse({ id: 'ns1' }))
    await getNamespace()
    expect(lastHeaders()['X-Citeck-Locale']).toBe('en')

    // The transports read the store imperatively for exactly this reason:
    // they are not components and have no render to re-subscribe on.
    useI18nStore.setState({ locale: 'ja' })
    fetchMock.mockResolvedValueOnce(jsonResponse({ id: 'ns1' }))
    await getNamespace()
    expect(lastHeaders()['X-Citeck-Locale']).toBe('ja')
  })
})

describe('the SSE stream states its locale in the query string', () => {
  const urls: string[] = []

  class FakeEventSource {
    onopen: (() => void) | null = null
    onmessage: ((e: MessageEvent) => void) | null = null
    onerror: (() => void) | null = null
    constructor(url: string) {
      urls.push(url)
    }
    addEventListener() {}
    close() {}
  }

  beforeEach(() => {
    urls.length = 0
    vi.stubGlobal('EventSource', FakeEventSource)
    useI18nStore.setState({ locale: 'en' })
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    useI18nStore.setState({ locale: 'en' })
  })

  it('carries ?locale= because EventSource cannot set a header', () => {
    useI18nStore.setState({ locale: 'pt' })
    connectEvents(() => {})
    expect(urls.at(-1)).toContain('locale=pt')
  })

  it('keeps lastSeq alongside it — the replay contract is unchanged', () => {
    useI18nStore.setState({ locale: 'fr' })
    connectEvents(() => {}, undefined, undefined, undefined, 42)
    const url = urls.at(-1)!
    const params = new URLSearchParams(url.slice(url.indexOf('?') + 1))
    expect(params.get('locale')).toBe('fr')
    expect(params.get('lastSeq')).toBe('42')
  })
})
