import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mockNuxtImport } from '@nuxt/test-utils/runtime'
import { useManagerApi } from '~/composables/useManagerApi'

const mocks = vi.hoisted(() => ({
  runtimeConfig: { public: { apiBase: '' } },
  requestURL: { protocol: 'http:', hostname: '192.168.60.5' },
  fetch: vi.fn()
}))

mockNuxtImport('useRuntimeConfig', () => () => mocks.runtimeConfig)
mockNuxtImport('useRequestURL', () => () => mocks.requestURL)

beforeEach(() => {
  mocks.runtimeConfig.public.apiBase = ''
  mocks.requestURL.protocol = 'http:'
  mocks.requestURL.hostname = '192.168.60.5'
  mocks.fetch.mockReset()
  vi.stubGlobal('$fetch', mocks.fetch)
})

describe('useManagerApi', () => {
  it('derives a LAN-safe API URL from the current host', () => {
    const { apiBase } = useManagerApi()
    expect(apiBase.value).toBe('http://192.168.60.5:8888')
  })

  it('prefers and normalizes an explicitly configured API URL', () => {
    mocks.runtimeConfig.public.apiBase = 'https://manager.example.test/'
    const { apiBase } = useManagerApi()
    expect(apiBase.value).toBe('https://manager.example.test')
  })

  it('sends credentialed requests and preserves caller options', async () => {
    mocks.fetch.mockResolvedValue({ ok: true })
    const { request } = useManagerApi()
    const result = await request('/api/v1/models', {
      method: 'POST',
      body: { model_id: 'coder' },
      headers: { 'X-Test': 'yes' }
    })
    expect(result).toEqual({ ok: true })
    expect(mocks.fetch).toHaveBeenCalledWith('http://192.168.60.5:8888/api/v1/models', {
      credentials: 'include',
      method: 'POST',
      body: { model_id: 'coder' },
      headers: { 'X-Test': 'yes' }
    })
  })

  it('propagates request failures', async () => {
    mocks.fetch.mockRejectedValue(new Error('network down'))
    const { request } = useManagerApi()
    await expect(request('/health')).rejects.toThrow('network down')
  })
})
