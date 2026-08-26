import { beforeEach, describe, expect, it, vi } from 'vitest'
import { useRequestURL, useRuntimeConfig } from '#app'
import { useManagerApi } from '~/composables/useManagerApi'

const fetchMock = vi.fn()

beforeEach(() => {
  const config = useRuntimeConfig()
  ;(config.public as any).apiBase = ''
  fetchMock.mockReset()
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

  it('sends credentialed requests and preserves caller options', async () => {
    fetchMock.mockResolvedValue({ ok: true })
    const { apiBase, request } = useManagerApi(fetchMock as any)
    const result = await request('/api/v1/models', {
      method: 'POST',
      body: { model_id: 'coder' },
      headers: { 'X-Test': 'yes' }
    })
    expect(result).toEqual({ ok: true })
    expect(fetchMock).toHaveBeenCalledWith(`${apiBase.value}/api/v1/models`, {
      credentials: 'include',
      method: 'POST',
      body: { model_id: 'coder' },
      headers: { 'X-Test': 'yes' }
    })
  })

  it('propagates request failures', async () => {
    fetchMock.mockRejectedValue(new Error('network down'))
    const { request } = useManagerApi(fetchMock as any)
    await expect(request('/health')).rejects.toThrow('network down')
  })
})
