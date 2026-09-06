import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import { PLAYGROUND_MAX_ATTACHMENT_BYTES, PLAYGROUND_MAX_ATTACHMENTS } from '~/utils/playgroundMessageContent'
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

async function activateTab(wrapper: any, text: string) {
  const tab = wrapper.findAll('[role="tab"]').find((candidate: any) => candidate.text().trim() === text)
  if (!tab) throw new Error(`Missing tab: ${text}`)
  await tab.trigger('pointerdown')
  await tab.trigger('click')
}

function promptSubmit(wrapper: any) {
  return wrapper.get('[data-testid="playground-prompt-submit"]')
}

async function sendPlayground(wrapper: any) {
  await promptSubmit(wrapper).trigger('click')
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
  sessionStorage.setItem('llamarack_management_token', 'management-playground')
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
          'X-LlamaRack-Request-ID': 'req-1',
          'X-LlamaRack-Instance': 'coder',
          'X-LlamaRack-Autoloaded': 'true',
          'X-LlamaRack-Upstream-Port': '9101'
        }
      }
    ))
    vi.stubGlobal('fetch', publicFetch)

    const wrapper = await mountSuspended(PlaygroundPage, { route: '/playground' })
    await flushPromises()
    expect(wrapper.text()).toContain('PLAYGROUND')
    expect(wrapper.text()).toContain('This Instance is not loaded — sending will trigger autoload through the gateway.')
    expect(wrapper.find('#playground-api-key').exists()).toBe(false)
    expect(wrapper.get('[data-testid="playground-thread-chrome"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="playground-mobile-quick-parameters"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="playground-mobile-parameters-toggle"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="playground-empty-state"]').text()).toContain('Type a prompt to start.')

    await wrapper.get('textarea[aria-label="Playground message"]').setValue('Explain this code')
    await sendPlayground(wrapper)
    await flushPromises()

    expect(publicFetch).toHaveBeenCalledTimes(1)
    const [url, init] = publicFetch.mock.calls[0]!
    expect(url).toBe('http://manager.test:8888/api/v1/playground/chat/completions')
    expect((init.headers as Record<string, string>).Authorization).toBe('Bearer management-playground')
    expect((init.headers as Record<string, string>)['X-LiteLLM-Session-ID']).toMatch(/^[0-9a-f-]{36}$/i)
    const body = JSON.parse(String(init.body))
    expect(body.model).toBe('coder')
    expect(body.messages.at(-1)).toEqual({ role: 'user', content: 'Explain this code' })
    expect(body.stream).toBe(true)
    expect(mocks.request).toHaveBeenCalledWith('/api/v1/observability/playground/req-1')

    expect(wrapper.text()).toContain('Hello world')
    expect(wrapper.text()).toContain('12 prompt · 24 completion (incl. reasoning) · 32.00 tok/s · ttft 150 ms')
    expect(wrapper.get('[data-testid="playground-parameters"]').exists()).toBe(true)
    expect(wrapper.get('[data-testid="playground-model-name"]').text()).toBe('Qwen Coder')
    await activateTab(wrapper, 'Response')
    expect(wrapper.text()).toContain('UNLOADED → STARTING → READY')
    expect(wrapper.text()).toContain('yes — autoload')
    expect(wrapper.text()).toContain('victim-a')
    expect(wrapper.text()).toContain('36 / 32768')
    expect(wrapper.text()).toContain('CUDA0 8.00 GiB')
    expect(wrapper.text()).toContain('x-llamarack-instance: coder')
    expect(wrapper.text()).not.toContain('x-llamarack-upstream-port')
    expect(sessionStorage.getItem('lcm-playground-api-key')).toBeNull()
    wrapper.unmount()
  })

  it('uses edited raw JSON as the next request source of truth', async () => {
    mocks.runtime.state = 'READY'
    mocks.request.mockResolvedValue({ ...diagnostic(), state_trace: ['READY'], evictions_triggered: [] })
    const publicFetch = vi.fn(async () => new Response(
      JSON.stringify({ choices: [{ message: { content: 'raw reply' } }] }),
      { status: 200, headers: { 'X-LlamaRack-Request-ID': 'req-1' } }
    ))
    vi.stubGlobal('fetch', publicFetch)

    const wrapper = await mountSuspended(PlaygroundPage, { route: '/playground' })
    await flushPromises()
    await activateTab(wrapper, 'Request')
    const raw = {
      model: 'coder',
      messages: [{ role: 'system', content: 'Be terse' }, { role: 'user', content: 'from raw JSON' }],
      temperature: 0.2,
      max_tokens: 9,
      stream: false
    }
    await wrapper.get('textarea[aria-label="Raw request JSON"]').setValue(JSON.stringify(raw))
    await sendPlayground(wrapper)
    await flushPromises()

    const sent = JSON.parse(String(publicFetch.mock.calls[0]![1].body))
    expect(sent).toEqual(raw)
    expect(wrapper.text()).toContain('Be terse')
    expect(wrapper.text()).toContain('from raw JSON')
    expect(wrapper.text()).toContain('raw reply')
    expect(wrapper.text()).toContain('Instance READY')
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
    await sendPlayground(wrapper)
    await flushPromises()
    expect(promptSubmit(wrapper).attributes('aria-label')).toBe('Stop generating')

    await promptSubmit(wrapper).trigger('click')
    await flushPromises()
    expect(seenSignal?.aborted).toBe(true)
    expect(wrapper.text()).toContain('Request stopped.')
    expect(wrapper.find('[data-testid="playground-empty-content"]').exists()).toBe(false)
    expect(promptSubmit(wrapper).exists()).toBe(true)
    wrapper.unmount()
  })

  it('rejects a missing management session and invalid raw JSON without bypassing the bridge', async () => {
    const publicFetch = vi.fn()
    vi.stubGlobal('fetch', publicFetch)
    sessionStorage.removeItem('llamarack_management_token')
    const wrapper = await mountSuspended(PlaygroundPage, { route: '/playground' })
    await flushPromises()

    await wrapper.get('textarea[aria-label="Playground message"]').setValue('ping')
    await sendPlayground(wrapper)
    expect(wrapper.text()).toContain('Management session is unavailable. Sign in again.')
    expect(publicFetch).not.toHaveBeenCalled()

    sessionStorage.setItem('llamarack_management_token', 'management-playground')
    await activateTab(wrapper, 'Request')
    await wrapper.get('textarea[aria-label="Raw request JSON"]').setValue('{bad json')
    await sendPlayground(wrapper)
    await flushPromises()
    expect(wrapper.text()).toContain('Request JSON is not valid.')
    expect(publicFetch).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('sends image attachments as OpenAI-compatible multimodal message content', async () => {
    mocks.request.mockResolvedValue(diagnostic())
    const publicFetch = vi.fn(async (_url: string, init: RequestInit) => new Response(
      'data: {"choices":[{"delta":{"content":"I see an image"}}]}\n\ndata: [DONE]\n\n',
      { status: 200, headers: { 'X-LlamaRack-Request-ID': 'req-image' } }
    ))
    vi.stubGlobal('fetch', publicFetch)
    vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:diagram-preview')
    vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {})

    vi.stubGlobal('FileReader', class {
      result: string | ArrayBuffer | null = null
      onload: ((this: FileReader, ev: ProgressEvent<FileReader>) => any) | null = null
      onerror: ((this: FileReader, ev: ProgressEvent<FileReader>) => any) | null = null
      readAsDataURL() {
        this.result = 'data:image/png;base64,ZmFrZQ=='
        this.onload?.call(this, {} as ProgressEvent<FileReader>)
      }
    })

    const wrapper = await mountSuspended(PlaygroundPage, { route: '/playground' })
    await flushPromises()

    const file = new File(['fake'], 'diagram.png', { type: 'image/png' })
    const input = wrapper.get('[data-testid="playground-file-input"]')
    Object.defineProperty(input.element, 'files', { value: [file] })
    await input.trigger('change')
    await flushPromises()

    expect(wrapper.text()).toContain('diagram.png')
    await wrapper.get('textarea[aria-label="Playground message"]').setValue('describe this image')
    await sendPlayground(wrapper)
    await flushPromises()

    expect(publicFetch).toHaveBeenCalledTimes(1)
    const body = JSON.parse(String(publicFetch.mock.calls[0]![1].body))
    expect(body.messages).toEqual([
      {
        role: 'user',
        content: [
          { type: 'text', text: 'describe this image' },
          { type: 'image_url', image_url: { url: 'data:image/png;base64,ZmFrZQ==' } }
        ]
      }
    ])
    expect(wrapper.text()).toContain('I see an image')
    wrapper.unmount()
  })

  it('rejects unsupported attachment types and oversized images', async () => {
    vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:preview')
    vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {})
    const wrapper = await mountSuspended(PlaygroundPage, { route: '/playground' })
    await flushPromises()

    const input = wrapper.get('[data-testid="playground-file-input"]')
    Object.defineProperty(input.element, 'files', { configurable: true, value: [new File(['bad'], 'notes.pdf', { type: 'application/pdf' })] })
    await input.trigger('change')
    await flushPromises()
    expect(wrapper.text()).toContain('Only image files can be attached in Playground.')

    const oversizedInput = wrapper.get('[data-testid="playground-file-input"]')
    Object.defineProperty(oversizedInput.element, 'files', { configurable: true, value: [new File([new Uint8Array(PLAYGROUND_MAX_ATTACHMENT_BYTES + 1)], 'huge.png', { type: 'image/png' })] })
    await oversizedInput.trigger('change')
    await flushPromises()
    expect(wrapper.text()).toContain('exceeds the 8 MiB attachment limit.')

    for (let index = 0; index < PLAYGROUND_MAX_ATTACHMENTS; index++) {
      const attachInput = wrapper.get('[data-testid="playground-file-input"]')
      Object.defineProperty(attachInput.element, 'files', { configurable: true, value: [new File([`img-${index}`], `img-${index}.png`, { type: 'image/png' })] })
      await attachInput.trigger('change')
      await flushPromises()
    }
    const limitInput = wrapper.get('[data-testid="playground-file-input"]')
    Object.defineProperty(limitInput.element, 'files', { configurable: true, value: [new File(['overflow'], 'overflow.png', { type: 'image/png' })] })
    await limitInput.trigger('change')
    await flushPromises()
    expect(wrapper.text()).toContain(`Playground supports up to ${PLAYGROUND_MAX_ATTACHMENTS} images per message.`)
    wrapper.unmount()
  })

  it('reuses one LiteLLM session for follow-up sends and omits it when reuse is turned off', async () => {
    mocks.runtime.state = 'READY'
    mocks.request.mockResolvedValue({ ...diagnostic(), state_trace: ['READY'], evictions_triggered: [] })
    const publicFetch = vi.fn(async () => new Response(
      'data: {"choices":[{"delta":{"content":"ok"}}]}\n\ndata: [DONE]\n\n',
      { status: 200, headers: { 'X-LlamaRack-Request-ID': 'req-session' } }
    ))
    vi.stubGlobal('fetch', publicFetch)

    const wrapper = await mountSuspended(PlaygroundPage, { route: '/playground' })
    await flushPromises()
    const sessionID = wrapper.get('[data-testid="playground-session-id"]').text().trim()
    expect(sessionID.length).toBeGreaterThan(8)
    expect(wrapper.text()).toContain('Reuse session')

    await wrapper.get('textarea[aria-label="Playground message"]').setValue('first turn')
    await sendPlayground(wrapper)
    await flushPromises()
    await wrapper.get('textarea[aria-label="Playground message"]').setValue('follow up')
    await sendPlayground(wrapper)
    await flushPromises()

    expect(publicFetch).toHaveBeenCalledTimes(2)
    const firstHeaders = publicFetch.mock.calls[0]![1]!.headers as Record<string, string>
    const secondHeaders = publicFetch.mock.calls[1]![1]!.headers as Record<string, string>
    expect(firstHeaders['X-LiteLLM-Session-ID']).toBe(sessionID)
    expect(secondHeaders['X-LiteLLM-Session-ID']).toBe(sessionID)
    expect(JSON.parse(String(publicFetch.mock.calls[0]![1]!.body)).session_id).toBeUndefined()
    expect(wrapper.get('[data-testid="playground-diagnostics-session"]').text()).toBe(sessionID)

    const reuseSwitch = wrapper.get('[data-testid="playground-reuse-session"]')
    const switchControl = reuseSwitch.find('[role="switch"]').exists()
      ? reuseSwitch.get('[role="switch"]')
      : reuseSwitch
    await switchControl.trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-testid="playground-session-id"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="playground-diagnostics-session"]').text()).toBe('—')

    await wrapper.get('textarea[aria-label="Playground message"]').setValue('ungrouped')
    await sendPlayground(wrapper)
    await flushPromises()
    const thirdHeaders = publicFetch.mock.calls[2]![1]!.headers as Record<string, string>
    expect(thirdHeaders['X-LiteLLM-Session-ID']).toBeUndefined()

    await switchControl.trigger('click')
    await flushPromises()
    const resumedSession = wrapper.get('[data-testid="playground-session-id"]').text().trim()
    expect(resumedSession.length).toBeGreaterThan(8)
    expect(resumedSession).not.toBe(sessionID)
    wrapper.unmount()
  })

  it('surfaces attachment read failures without calling the gateway', async () => {
    vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:preview')
    vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {})
    vi.stubGlobal('FileReader', class {
      onload: ((this: FileReader, ev: ProgressEvent<FileReader>) => any) | null = null
      onerror: ((this: FileReader, ev: ProgressEvent<FileReader>) => any) | null = null
      readAsDataURL() {
        this.onerror?.call(this, {} as ProgressEvent<FileReader>)
      }
    })
    const publicFetch = vi.fn()
    vi.stubGlobal('fetch', publicFetch)

    const wrapper = await mountSuspended(PlaygroundPage, { route: '/playground' })
    await flushPromises()
    const input = wrapper.get('[data-testid="playground-file-input"]')
    Object.defineProperty(input.element, 'files', { configurable: true, value: [new File(['x'], 'broken.png', { type: 'image/png' })] })
    await input.trigger('change')
    await flushPromises()
    await sendPlayground(wrapper)
    await flushPromises()

    expect(publicFetch).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('Unable to read one or more attachments.')
    wrapper.unmount()
  })
})
