import { beforeEach, describe, expect, it, vi } from 'vitest'
import { useRequestURL, useRuntimeConfig } from '#app'
import { clearManagementToken, readManagementToken, storeManagementToken, useManagerApi } from '~/composables/useManagerApi'

const fetchMock = vi.fn()

beforeEach(() => {
  const config = useRuntimeConfig()
  ;(config.public as any).apiBase = ''
  fetchMock.mockReset()
  clearManagementToken()
})

describe('useManagerApi', () => {
  it('uses the current request origin when no API URL is configured', () => {
    const requestURL = useRequestURL()
    const { apiBase } = useManagerApi(fetchMock as any)
    expect(apiBase.value).toBe(requestURL.origin)
  })

  it('prefers and normalizes an explicitly configured API URL', () => {
    const config = useRuntimeConfig()
    ;(config.public as any).apiBase = 'https://manager.example.test/'
    const { apiBase } = useManagerApi(fetchMock as any)
    expect(apiBase.value).toBe('https://manager.example.test')
  })

  it('preserves request options and caller headers in a Headers object', async () => {
    fetchMock.mockResolvedValue({ ok: true })
    const { apiBase, request } = useManagerApi(fetchMock as any)
    const result = await request('/api/v1/models', {
      method: 'POST',
      body: { model_id: 'coder' },
      headers: { 'X-Test': 'yes' }
    })
    expect(result).toEqual({ ok: true })
    expect(fetchMock).toHaveBeenCalledTimes(1)
    const [url, options] = fetchMock.mock.calls[0]!
    expect(url).toBe(`${apiBase.value}/api/v1/models`)
    expect(options.credentials).toBeUndefined()
    expect(options.method).toBe('POST')
    expect(options.body).toEqual({ model_id: 'coder' })
    expect(options.headers).toBeInstanceOf(Headers)
    expect(options.headers.get('X-Test')).toBe('yes')
  })

  it('adds the management bearer token to reads and mutations without CSRF headers', async () => {
    fetchMock.mockResolvedValue({ ok: true })
    storeManagementToken('session-jwt', false)
    const { request } = useManagerApi(fetchMock as any)

    await request('/api/v1/models', { method: 'POST' })
    let options = fetchMock.mock.calls[0]![1]
    expect(options.headers.get('Authorization')).toBe('Bearer session-jwt')
    expect(options.headers.get('X-CSRF-Token')).toBeNull()

    await request('/api/v1/models')
    options = fetchMock.mock.calls[1]![1]
    expect(options.headers.get('Authorization')).toBe('Bearer session-jwt')
  })

  it('preserves an explicit Authorization header and switches storage by remember-me choice', async () => {
    fetchMock.mockResolvedValue({ ok: true })
    storeManagementToken('remembered', true)
    expect(readManagementToken()).toBe('remembered')
    expect(window.localStorage.getItem('llamarack_management_token')).toBe('remembered')
    expect(window.sessionStorage.getItem('llamarack_management_token')).toBeNull()

    const { request } = useManagerApi(fetchMock as any)
    await request('/api/v1/me', { headers: { Authorization: 'Bearer explicit' } })
    expect(fetchMock.mock.calls[0]![1].headers.get('Authorization')).toBe('Bearer explicit')

    storeManagementToken('temporary', false)
    expect(readManagementToken()).toBe('temporary')
    expect(window.sessionStorage.getItem('llamarack_management_token')).toBe('temporary')
    expect(window.localStorage.getItem('llamarack_management_token')).toBeNull()
    clearManagementToken()
    expect(readManagementToken()).toBe('')
  })

  it('clears both stores when an empty management token is saved', () => {
    window.sessionStorage.setItem('llamarack_management_token', 'stale-session')
    window.localStorage.setItem('llamarack_management_token', 'stale-local')

    storeManagementToken('', true)

    expect(window.sessionStorage.getItem('llamarack_management_token')).toBeNull()
    expect(window.localStorage.getItem('llamarack_management_token')).toBeNull()
    expect(readManagementToken()).toBe('')
  })

  it('propagates request failures', async () => {
    fetchMock.mockRejectedValue(new Error('network down'))
    const { request } = useManagerApi(fetchMock as any)
    await expect(request('/health')).rejects.toThrow('network down')
  })
})
