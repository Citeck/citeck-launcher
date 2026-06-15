import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import {
  ApiError,
  getNamespace,
  getNamespaceEdit,
  putNamespaceEdit,
  putAppConfig,
  putAppFile,
  postGitSkipPull,
  postImportSnapshot,
  deleteSecret,
  type NamespaceEditDto,
} from './api'
import { useAuthGateStore } from './authGate'

/** Minimal Response stand-in for the fetch stub. */
function jsonResponse(body: unknown, status = 200, statusText = 'OK'): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    statusText,
    json: async () => body,
    text: async () => JSON.stringify(body),
  } as unknown as Response
}

/** Non-JSON (e.g. HTML) error body: json() rejects like the real Response. */
function textResponse(text: string, status: number, statusText: string): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    statusText,
    json: async () => {
      throw new SyntaxError('Unexpected token')
    },
    text: async () => text,
  } as unknown as Response
}

const fetchMock = vi.fn()

/** The RequestInit of the most recent fetch call. */
function lastInit(): RequestInit {
  const call = fetchMock.mock.calls.at(-1)
  expect(call).toBeDefined()
  return call![1] as RequestInit
}

function lastUrl(): string {
  return fetchMock.mock.calls.at(-1)![0] as string
}

describe('api request core (rawRequest/request via public helpers)', () => {
  beforeEach(() => {
    vi.stubGlobal('fetch', fetchMock)
    fetchMock.mockReset()
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('GET: prefixes API_BASE, sends Accept, no CSRF header, no body', async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse({ id: 'ns1' }))
    const res = await getNamespace()
    expect(res).toEqual({ id: 'ns1' })
    expect(lastUrl()).toBe('/api/v1/namespace')
    const init = lastInit()
    expect(init.method).toBe('GET')
    const headers = init.headers as Record<string, string>
    expect(headers.Accept).toBe('application/json')
    expect(headers['X-Citeck-CSRF']).toBeUndefined()
    expect(init.body).toBeUndefined()
  })

  it('POST with JSON body: CSRF header + application/json + serialized body', async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse({ success: true, message: '' }))
    await postGitSkipPull('gitlab.example.com', 60)
    expect(lastUrl()).toBe('/api/v1/git/skip-pull')
    const init = lastInit()
    expect(init.method).toBe('POST')
    const headers = init.headers as Record<string, string>
    expect(headers['X-Citeck-CSRF']).toBe('1')
    expect(headers['Content-Type']).toBe('application/json')
    expect(init.body).toBe(JSON.stringify({ host: 'gitlab.example.com', durationSeconds: 60 }))
  })

  it('PUT with string body: raw body + caller contentType + CSRF header', async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse({ success: true, message: '' }))
    await putAppConfig('emodel', 'image: x\n')
    expect(lastUrl()).toBe('/api/v1/apps/emodel/config')
    const init = lastInit()
    expect(init.method).toBe('PUT')
    const headers = init.headers as Record<string, string>
    expect(headers['X-Citeck-CSRF']).toBe('1')
    expect(headers['Content-Type']).toBe('text/yaml')
    expect(init.body).toBe('image: x\n')
  })

  it('string body without contentType defaults to text/plain', async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse({ success: true, message: '' }))
    await putAppFile('emodel', './conf/app.yml', 'hello')
    expect(lastUrl()).toBe('/api/v1/apps/emodel/files/conf/app.yml')
    const headers = lastInit().headers as Record<string, string>
    expect(headers['Content-Type']).toBe('text/plain')
    expect(lastInit().body).toBe('hello')
  })

  it('FormData body is passed as-is without a Content-Type header', async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse({ success: true, message: '' }))
    const file = new File(['zipbytes'], 'snap.zip', { type: 'application/zip' })
    await postImportSnapshot(file)
    expect(lastUrl()).toBe('/api/v1/snapshots/import')
    const init = lastInit()
    expect(init.method).toBe('POST')
    expect(init.body).toBeInstanceOf(FormData)
    const headers = init.headers as Record<string, string>
    // Browser must set the multipart boundary itself.
    expect(headers['Content-Type']).toBeUndefined()
    expect(headers['X-Citeck-CSRF']).toBe('1')
  })

  it('DELETE carries the CSRF header', async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse({ success: true, message: '' }))
    await deleteSecret('my secret')
    expect(lastUrl()).toBe('/api/v1/secrets/my%20secret')
    const init = lastInit()
    expect(init.method).toBe('DELETE')
    expect((init.headers as Record<string, string>)['X-Citeck-CSRF']).toBe('1')
  })

  it('non-2xx with JSON {code,message} body throws ApiError carrying both', async () => {
    fetchMock.mockResolvedValueOnce(
      jsonResponse({ message: 'encryption is not set up', code: 'ENCRYPTION_NOT_SET_UP' }, 409, 'Conflict'),
    )
    const err = await getNamespace().catch((e: unknown) => e)
    expect(err).toBeInstanceOf(ApiError)
    const apiErr = err as ApiError
    expect(apiErr.message).toBe('encryption is not set up')
    expect(apiErr.code).toBe('ENCRYPTION_NOT_SET_UP')
    expect(apiErr.status).toBe(409)
  })

  it('non-2xx with a non-JSON body falls back to statusText', async () => {
    fetchMock.mockResolvedValueOnce(textResponse('<html>boom</html>', 502, 'Bad Gateway'))
    const err = await getNamespace().catch((e: unknown) => e)
    expect(err).toBeInstanceOf(ApiError)
    const apiErr = err as ApiError
    expect(apiErr.message).toBe('Bad Gateway')
    expect(apiErr.code).toBe('')
    expect(apiErr.status).toBe(502)
  })

  it('401 AUTH_REQUIRED raises the auth gate and still throws to the caller', async () => {
    useAuthGateStore.setState({ required: false })
    fetchMock.mockResolvedValueOnce(
      jsonResponse({ message: 'auth required', code: 'AUTH_REQUIRED' }, 401, 'Unauthorized'),
    )
    const err = await getNamespace().catch((e: unknown) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect((err as ApiError).status).toBe(401)
    expect((err as ApiError).code).toBe('AUTH_REQUIRED')
    expect(useAuthGateStore.getState().required).toBe(true)
  })

  it('a non-AUTH_REQUIRED 401 does not raise the auth gate', async () => {
    useAuthGateStore.setState({ required: false })
    fetchMock.mockResolvedValueOnce(jsonResponse({ message: 'nope', code: 'OTHER' }, 401, 'Unauthorized'))
    await getNamespace().catch(() => undefined)
    expect(useAuthGateStore.getState().required).toBe(false)
  })

  it('namespace edit endpoints are scoped by namespace id', async () => {
    const dto: NamespaceEditDto = {
      name: 'Dev',
      bundleRepo: 'community',
      bundleKey: 'LATEST',
      authType: 'BASIC',
      users: ['admin'],
      host: '',
      port: 0,
      tlsEnabled: false,
      pgAdminEnabled: false,
    }
    fetchMock.mockResolvedValueOnce(jsonResponse(dto))
    await getNamespaceEdit('ns one')
    expect(lastUrl()).toBe('/api/v1/namespaces/ns%20one/edit')
    expect(lastInit().method).toBe('GET')

    fetchMock.mockResolvedValueOnce(jsonResponse({ success: true, message: '' }))
    await putNamespaceEdit('ns one', dto)
    expect(lastUrl()).toBe('/api/v1/namespaces/ns%20one/edit')
    const init = lastInit()
    expect(init.method).toBe('PUT')
    expect(init.body).toBe(JSON.stringify(dto))
    expect((init.headers as Record<string, string>)['X-Citeck-CSRF']).toBe('1')
  })
})
