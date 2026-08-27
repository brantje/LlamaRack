import { beforeEach, describe, expect, it, vi } from 'vitest'
import { useRequestURL, useRuntimeConfig } from '#app'
import { useManagerApi } from '~/composables/useManagerApi'

const fetchMock = vi.fn()

beforeEach(() => {
  const config = useRuntimeConfig()
  ;(config.public as any).apiBase = ''
  fetchMock.mockReset()
  document.cookie = 'lcm_csrf=; Max-Age=0; path=/'
})

describe('useManagerApi', () => {
  it('derives the API URL from the current request host', () => {
    const requestURL = useRequestURL()
    const { apiBase } = useManagerApi(fetchMock as any)
    expect(apiBase.value).toBe(`${requestURL.protocol}//${requestURL.hostname}:8888`)
  })

  it('prefers and normalizes an explicitly configured API URL', () => {
    const config = useRuntimeConfig()
    ;(config.public as any).apiBase = 'https://manager.example.test/'
    const { apiBase } = useManagerApi(fetchMock as any)
    expect(apiBase.value).toBe('https://manager.example.test')
  })

  it('sends credentialed requests and preserves caller headers in a Headers object', async () => {
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
    expect(options.credentials).toBe('include')
    expect(options.method).toBe('POST')
    expect(options.body).toEqual({ model_id: 'coder' })
    expect(options.headers).toBeInstanceOf(Headers)
    expect(options.headers.get('X-Test')).toBe('yes')
  })

  it('adds the CSRF cookie value to management mutations but not reads', async () => {
    fetchMock.mockResolvedValue({ ok: true })
    document.cookie = 'lcm_csrf=csrf%20token; path=/'
    const { request } = useManagerApi(fetchMock as any)

    await request('/api/v1/models', { method: 'POST' })
    let options = fetchMock.mock.calls[0]![1]
    expect(options.headers.get('X-CSRF-Token')).toBe('csrf token')

    await request('/api/v1/models')
    options = fetchMock.mock.calls[1]![1]
    expect(options.headers.get('X-CSRF-Token')).toBeNull()
  })

  it('propagates request failures', async () => {
    fetchMock.mockRejectedValue(new Error('network down'))
    const { request } = useManagerApi(fetchMock as any)
    await expect(request('/health')).rejects.toThrow('network down')
  })
})
