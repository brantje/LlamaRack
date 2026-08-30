import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import PlaygroundPage from '~/pages/playground.vue'

const mocks = vi.hoisted(() => ({
  request: vi.fn(),
  runtime: { instance_id: 'coder', model_id: 'model-1', state: 'UNLOADED', pid: 77, port: 9101 },
  manager: null as any
}))

mocks.manager = {
  apiBase: { value: 'http://manager.test:8888' },
  instances: { value: [{
    id: 'coder', model_id: 'model-1', name: 'Coder', enabled: true, autoload_enabled: true, always_on: false,
    priority: 'normal', eviction_enabled: true, idle_unload_seconds: 300, gpu_mode: 'auto'
  }] },
  models: { value: [{ id: 'model-1', name: 'Qwen Coder', gguf_path: 'qwen.gguf', total_bytes: 1, context_length: 32768 }] },
  runtimeForInstance: vi.fn(() => mocks.runtime),
  telemetryForInstance: vi.fn(() => ({
    instance_id: 'coder', pid: 77, gpu_devices: ['CUDA0'], collected_at: '2026-08-30T00:00:00Z',
    gpus: [{ device_id: 'CUDA0', vram_used_bytes: 8 * 1024 ** 3 }], vram_used_bytes: 8 * 1024 ** 3
  })),
  instanceState: vi.fn(() => mocks.runtime.state),
  request: mocks.request
}

mockNuxtImport('useManager', () => () => mocks.manager)

function button(wrapper: any, text: string) {
  const found = wrapper.findAll('button').find((candidate: any) => candidate.text().trim() === text)
  if (!found) throw new Error(`Missing button: ${text}`)
  return found
}

function diagnostic() {
  return {
    request: {
      request_id: 'req-1', instance_id: 'coder', status_code: 200, result: 'success', duration_ms: 900,
      ttft_ms: 150, prompt_tokens: 12, generated_tokens: 24, total_tokens: 36, tokens_per_second: 32,
      load_duration_ms: 420, autoloaded: true
    },
    state_trace: ['UNLOADED', 'STARTING', 'READY'],
    evictions_triggered: ['victim-a']
  }
}

beforeEach(() => {
  mocks.request.mockReset()
  mocks.runtime.state = 'UNLOADED'
  mocks.runtime.port = 9101
  sessionStorage.clear()
  localStorage.clear()
  sessionStorage.setItem('lcm_management_token', 'management-playground')
  vi.unstubAllGlobals()
})

describe('Playground', () => {
  it('uses the management bridge, streams output and loads correlated gateway diagnostics', async () => {
    mocks.request.mockResolvedValue(diagnostic())
    const publicFetch = vi.fn(async (_url: string, init: RequestInit) => new Response(
      'data: {"choices":[{"delta":{"content":"Hello"}}]}\n\ndata: {"choices":[{"delta":{"content":" world"}}]}\n\ndata: [DONE]\n\n',
      {
        status: 200,
        headers: {
          'Content-Type': 'text/event-stream',
          'X-LlamaCPP-Manager-Request-ID': 'req-1',
          'X-LlamaCPP-Manager-Instance': 'coder',
          'X-LlamaCPP-Manager-Autoloaded': 'true',
          'X-LlamaCPP-Manager-Upstream-Port': '9101'
        }
      }
    ))
    vi.stubGlobal('fetch', publicFetch)

    const wrapper = await mountSuspended(PlaygroundPage, { route: '/playground' })
    await flushPromises()
    expect(wrapper.text()).toContain('PLAYGROUND')
    expect(wrapper.text()).toContain('This Instance is not loaded — sending will trigger autoload through the gateway.')
    expect(wrapper.find('#playground-api-key').exists()).toBe(false)

    await wrapper.get('textarea[aria-label="Playground message"]').setValue('Explain this code')
    await button(wrapper, 'Send').trigger('click')
    await flushPromises()

    expect(publicFetch).toHaveBeenCalledTimes(1)
    const [url, init] = publicFetch.mock.calls[0]!
    expect(url).toBe('http://manager.test:8888/api/v1/playground/chat/completions')
    expect((init.headers as Record<string, string>).Authorization).toBe('Bearer management-playground')
    const body = JSON.parse(String(init.body))
    expect(body.model).toBe('coder')
    expect(body.messages.at(-1)).toEqual({ role: 'user', content: 'Explain this code' })
    expect(body.stream).toBe(true)
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/observability/playground/req-1')

    expect(wrapper.text()).toContain('Hello world')
    expect(wrapper.text()).toContain('12 prompt · 24 completion · 32.00 tok/s · ttft 150 ms')
    expect(wrapper.text()).toContain('UNLOADED → STARTING → READY')
    expect(wrapper.text()).toContain('yes — autoload')
    expect(wrapper.text()).toContain('victim-a')
    expect(wrapper.text()).toContain('36 / 32768')
    expect(wrapper.text()).toContain('CUDA0 8.00 GiB')
    expect(wrapper.text()).toContain('x-llamacpp-manager-upstream-port: 9101')
    expect(sessionStorage.getItem('lcm-playground-api-key')).toBeNull()
    wrapper.unmount()
  })

  it('uses edited raw JSON as the next request source of truth', async () => {
    mocks.runtime.state = 'READY'
    mocks.request.mockResolvedValue({ ...diagnostic(), state_trace: ['READY'], evictions_triggered: [] })
    const publicFetch = vi.fn(async () => new Response(
      JSON.stringify({ choices: [{ message: { content: 'raw reply' } }] }),
      { status: 200, headers: { 'X-LlamaCPP-Manager-Request-ID': 'req-1' } }
    ))
    vi.stubGlobal('fetch', publicFetch)

    const wrapper = await mountSuspended(PlaygroundPage, { route: '/playground' })
    await flushPromises()
    await button(wrapper, 'Request').trigger('click')
    const raw = {
      model: 'coder',
      messages: [{ role: 'system', content: 'Be terse' }, { role: 'user', content: 'from raw JSON' }],
      temperature: 0.2,
      max_tokens: 9,
      stream: false
    }
    await wrapper.get('textarea[aria-label="Raw request JSON"]').setValue(JSON.stringify(raw))
    await button(wrapper, 'Send').trigger('click')
    await flushPromises()

    const sent = JSON.parse(String(publicFetch.mock.calls[0]![1].body))
    expect(sent).toEqual(raw)
    expect(wrapper.text()).toContain('Be terse')
    expect(wrapper.text()).toContain('from raw JSON')
    expect(wrapper.text()).toContain('raw reply')
    expect(wrapper.text()).toContain('READY')
    wrapper.unmount()
  })

  it('cancels the in-flight bridged request with Stop', async () => {
    mocks.runtime.state = 'READY'
    let seenSignal: AbortSignal | undefined
    const publicFetch = vi.fn((_url: string, init: RequestInit) => new Promise<Response>((_resolve, reject) => {
      seenSignal = init.signal as AbortSignal
      seenSignal.addEventListener('abort', () => reject(new DOMException('Aborted', 'AbortError')))
    }))
    vi.stubGlobal('fetch', publicFetch)

    const wrapper = await mountSuspended(PlaygroundPage, { route: '/playground' })
    await flushPromises()
    await wrapper.get('textarea[aria-label="Playground message"]').setValue('long response')
    await button(wrapper, 'Send').trigger('click')
    await flushPromises()
    expect(button(wrapper, 'Stop').exists()).toBe(true)

    await button(wrapper, 'Stop').trigger('click')
    await flushPromises()
    expect(seenSignal?.aborted).toBe(true)
    expect(wrapper.text()).toContain('Request stopped.')
    expect(button(wrapper, 'Send').exists()).toBe(true)
    wrapper.unmount()
  })

  it('rejects a missing management session and invalid raw JSON without bypassing the bridge', async () => {
    const publicFetch = vi.fn()
    vi.stubGlobal('fetch', publicFetch)
    sessionStorage.removeItem('lcm_management_token')
    const wrapper = await mountSuspended(PlaygroundPage, { route: '/playground' })
    await flushPromises()

    await button(wrapper, 'Send').trigger('click')
    expect(wrapper.text()).toContain('Management session is unavailable. Sign in again.')
    expect(publicFetch).not.toHaveBeenCalled()

    sessionStorage.setItem('lcm_management_token', 'management-playground')
    await button(wrapper, 'Request').trigger('click')
    await wrapper.get('textarea[aria-label="Raw request JSON"]').setValue('{bad json')
    await button(wrapper, 'Send').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('Request JSON is not valid.')
    expect(publicFetch).not.toHaveBeenCalled()
    wrapper.unmount()
  })
})
