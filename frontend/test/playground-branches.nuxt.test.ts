import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import PlaygroundPage from '~/pages/playground.vue'

const mocks = vi.hoisted(() => ({
  request: vi.fn(),
  runtime: { instance_id: 'coder', model_id: 'model-1', state: 'READY', pid: 77, port: 9101 } as any,
  telemetry: { instance_id: 'coder', pid: 77, gpu_devices: ['CUDA0'], collected_at: '2026-08-30T00:00:00Z', gpus: [] as any[], vram_used_bytes: 4 * 1024 ** 3 } as any,
  manager: null as any
}))

mocks.manager = {
  apiBase: { value: 'http://manager.test:8888' },
  instances: { value: [] as any[] },
  models: { value: [] as any[] },
  runtimeForInstance: vi.fn(() => mocks.runtime),
  telemetryForInstance: vi.fn(() => mocks.telemetry),
  instanceState: vi.fn(() => mocks.runtime.state),
  request: mocks.request
}

mockNuxtImport('useManager', () => () => mocks.manager)

function instance(id = 'coder', enabled = true) {
  return {
    id, model_id: 'model-1', name: id, enabled, autoload_enabled: true, always_on: false,
    priority: 'normal', eviction_enabled: true, idle_unload_seconds: 300, gpu_mode: 'auto'
  }
}

function resetManager() {
  mocks.manager.instances.value = [instance()]
  mocks.manager.models.value = [{ id: 'model-1', name: 'Qwen Coder', gguf_path: 'qwen.gguf', total_bytes: 1, context_length: 32768 }]
  mocks.runtime = { instance_id: 'coder', model_id: 'model-1', state: 'READY', pid: 77, port: 9101 }
  mocks.telemetry = { instance_id: 'coder', pid: 77, gpu_devices: ['CUDA0'], collected_at: '2026-08-30T00:00:00Z', gpus: [], vram_used_bytes: 4 * 1024 ** 3 }
  mocks.request.mockReset()
  sessionStorage.clear()
  localStorage.clear()
  sessionStorage.setItem('llamarack_management_token', 'management-branch')
  vi.unstubAllGlobals()
}

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

async function prepareRaw(wrapper: any, body: unknown) {
  await activateTab(wrapper, 'Request')
  await wrapper.get('textarea[aria-label="Raw request JSON"]').setValue(JSON.stringify(body))
}

beforeEach(resetManager)

describe('Playground edge branches', () => {
  it('adopts every supported raw parameter and preserves raw JSON authority', async () => {
    mocks.request.mockResolvedValue({
      request: { instance_id: 'coder', status_code: 200, result: 'success', duration_ms: 2200, prompt_tokens: 0, generated_tokens: 0, total_tokens: 0, load_duration_ms: 0, autoloaded: false },
      state_trace: ['READY'], evictions_triggered: []
    })
    const publicFetch = vi.fn(async () => new Response(JSON.stringify({ choices: [{ text: 'legacy text choice' }] }), {
      status: 200,
      headers: { 'X-LlamaRack-Request-ID': 'raw-all', 'X-LlamaRack-Instance': 'coder', 'X-LlamaRack-Upstream-Port': '9101' }
    }))
    vi.stubGlobal('fetch', publicFetch)

    const wrapper = await mountSuspended(PlaygroundPage, { route: '/playground' })
    const raw = {
      model: 'coder',
      messages: [
        { role: 'system', content: 'System rule' },
        { role: 'user', content: 'hello' },
        { role: 'tool', content: 'ignored role' },
        { role: 'assistant', content: 42 }
      ],
      temperature: 0.15, top_p: 0.8, max_tokens: 64, seed: 7, top_k: 8, min_p: 0.02, repeat_penalty: 1.2,
      stop: ['END', 'STOP'], stream: false
    }
    await prepareRaw(wrapper, raw)
    await sendPlayground(wrapper)
    await flushPromises()

    expect(JSON.parse(String(publicFetch.mock.calls[0]![1].body))).toEqual(raw)
    expect(wrapper.text()).toContain('System rule')
    expect(wrapper.text()).toContain('legacy text choice')
    expect(wrapper.text()).toContain('2.20 s')
    expect(wrapper.text()).toContain('—')
    await activateTab(wrapper, 'Response')
    expect(wrapper.text()).toContain('x-llamarack-instance: coder')
    expect(wrapper.text()).not.toContain('x-llamarack-upstream-port')
    wrapper.unmount()
  })

  it('rejects non-object and unknown-instance raw requests', async () => {
    const publicFetch = vi.fn()
    vi.stubGlobal('fetch', publicFetch)
    const wrapper = await mountSuspended(PlaygroundPage, { route: '/playground' })
    await activateTab(wrapper, 'Request')

    for (const raw of ['[]', 'null']) {
      await wrapper.get('textarea[aria-label="Raw request JSON"]').setValue(raw)
      await sendPlayground(wrapper)
      await flushPromises()
      expect(wrapper.text()).toContain('Request JSON must be an object.')
    }

    await wrapper.get('textarea[aria-label="Raw request JSON"]').setValue(JSON.stringify({ model: 'missing', messages: [] }))
    await sendPlayground(wrapper)
    await flushPromises()
    expect(wrapper.text()).toContain('Unknown Instance “missing”.')
    expect(publicFetch).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('formats structured, string, fallback, text and empty gateway errors', async () => {
    const publicFetch = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({ error: { message: 'nested failure' } }), { status: 400 }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ error: 'string failure' }), { status: 400 }))
      .mockResolvedValueOnce(new Response('{}', { status: 503 }))
      .mockResolvedValueOnce(new Response('plain failure', { status: 502 }))
      .mockResolvedValueOnce(new Response('', { status: 500 }))
    vi.stubGlobal('fetch', publicFetch)
    const wrapper = await mountSuspended(PlaygroundPage, { route: '/playground' })

    const expected = ['nested failure', 'string failure', 'Request failed with HTTP 503', 'plain failure', 'Request failed with HTTP 500']
    for (const message of expected) {
      await wrapper.get('textarea[aria-label="Playground message"]').setValue('error case')
      await sendPlayground(wrapper)
      await flushPromises()
      expect(wrapper.text()).toContain(message)
      expect(wrapper.text()).toContain('Last request failed')
      expect(wrapper.text()).not.toContain('Completed')
    }
    expect(publicFetch).toHaveBeenCalledTimes(5)
    wrapper.unmount()
  })

  it('handles SSE variants, a bodyless stream and a generic network failure', async () => {
    const publicFetch = vi.fn()
      .mockResolvedValueOnce(new Response(null, { status: 200 }))
      .mockResolvedValueOnce(new Response([
        ': keepalive',
        'data:',
        'data: [DONE]',
        'data: {bad json',
        'data: {"choices":[{"message":{"content":"message fallback"}}]}',
        'data: {"choices":[{"text":" text fallback"}]}',
        'data: {"choices":[{"delta":{"content":7}}]}'
      ].join('\n') + '\n', { status: 200 }))
      .mockRejectedValueOnce({})
    vi.stubGlobal('fetch', publicFetch)
    const wrapper = await mountSuspended(PlaygroundPage, { route: '/playground' })

    await wrapper.get('textarea[aria-label="Playground message"]').setValue('stream variants')
    await sendPlayground(wrapper)
    await flushPromises()
    expect(wrapper.text()).toContain('Completed')

    await wrapper.get('textarea[aria-label="Playground message"]').setValue('stream variants')
    await sendPlayground(wrapper)
    await flushPromises()
    expect(wrapper.text()).toContain('message fallback text fallback')

    await wrapper.get('textarea[aria-label="Playground message"]').setValue('stream variants')
    await sendPlayground(wrapper)
    await flushPromises()
    expect(wrapper.text()).toContain('Inference request failed.')
    expect(wrapper.text()).toContain('Last request failed')
    expect(wrapper.text()).not.toContain('Completed')
    wrapper.unmount()
  })

  it('retries diagnostics, uses device-wide GPU telemetry and shows sparse metric fallbacks', async () => {
    mocks.manager.models.value = [{ id: 'model-1', name: 'No Context', gguf_path: 'x.gguf', total_bytes: 1 }]
    mocks.telemetry = { instance_id: 'coder', pid: 77, gpu_devices: ['CUDA1'], collected_at: '2026-08-30T00:00:00Z', vram_used_bytes: 3 * 1024 ** 3 }
    mocks.request
      .mockRejectedValueOnce(new Error('not retained yet'))
      .mockResolvedValueOnce({
        request: { instance_id: '', status_code: 200, result: 'success', duration_ms: 800, prompt_tokens: 0, generated_tokens: 0, total_tokens: 0, load_duration_ms: 0, autoloaded: false },
        state_trace: [], evictions_triggered: []
      })
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ choices: [{ message: { content: 'done' } }] }), {
      status: 200,
      headers: { 'X-LlamaRack-Request-ID': 'retry-me' }
    })))

    const wrapper = await mountSuspended(PlaygroundPage, { route: '/playground' })
    await prepareRaw(wrapper, { model: 'coder', messages: [], stream: false })
    await sendPlayground(wrapper)
    await new Promise(resolve => setTimeout(resolve, 250))
    await flushPromises()

    expect(mocks.request).toHaveBeenCalledTimes(2)
    expect(wrapper.text()).toContain('CUDA1 · 3.00 GiB')
    expect(wrapper.text()).toContain('none')
    expect(wrapper.text()).toContain('no')
    wrapper.unmount()
  })

  it('surfaces exhausted diagnostics retries without replacing the completed response', async () => {
    mocks.request.mockRejectedValue(new Error('diagnostics unavailable'))
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ choices: [{ message: { content: 'answer survived' } }] }), {
      status: 200,
      headers: { 'X-LlamaRack-Request-ID': 'missing-diag' }
    })))

    const wrapper = await mountSuspended(PlaygroundPage, { route: '/playground' })
    await prepareRaw(wrapper, { model: 'coder', messages: [], stream: false })
    await sendPlayground(wrapper)
    await new Promise(resolve => setTimeout(resolve, 800))
    await flushPromises()

    expect(mocks.request).toHaveBeenCalledTimes(6)
    expect(wrapper.text()).toContain('answer survived')
    expect(wrapper.text()).toContain('diagnostics unavailable')
    wrapper.unmount()
  })

  it('covers clipboard success/failure, clear and keyboard send behavior without a Playground credential field', async () => {
    const writeText = vi.fn()
      .mockResolvedValueOnce(undefined)
      .mockResolvedValueOnce(undefined)
      .mockRejectedValueOnce(new Error('denied'))
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } })
    vi.stubGlobal('fetch', vi.fn(async () => new Response(null, { status: 200 })))

    const wrapper = await mountSuspended(PlaygroundPage, { route: '/playground' })
    await flushPromises()
    expect(wrapper.find('#playground-api-key').exists()).toBe(false)

    await button(wrapper, 'Copy as curl').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('curl copied.')
    expect(String(writeText.mock.calls[0]![0])).toContain('$LLAMA_API_KEY')
    expect(String(writeText.mock.calls[0]![0])).toContain('X-LiteLLM-Session-ID')

    await button(wrapper, 'Copy SDK example').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('SDK example copied.')
    const sdk = String(writeText.mock.calls[1]![0])
    expect(sdk).toContain('import json')
    expect(sdk).toContain('json.loads(')
    expect(sdk).toContain('client.chat.completions.create(**body')
    expect(sdk).toContain('X-LiteLLM-Session-ID')

    await button(wrapper, 'Copy SDK example').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('Unable to copy sdk example.')

    const composer = wrapper.get('textarea[aria-label="Playground message"]')
    await composer.setValue('keyboard message')
    await composer.trigger('keydown', { key: 'Enter', shiftKey: true })
    expect((globalThis.fetch as any).mock.calls).toHaveLength(0)
    await composer.trigger('keydown', { key: 'Enter', shiftKey: false })
    await flushPromises()
    expect((globalThis.fetch as any).mock.calls).toHaveLength(1)

    await wrapper.get('[data-testid="playground-clear-conversation"]').trigger('click')
    await flushPromises()
    const confirm = [...document.body.querySelectorAll<HTMLButtonElement>('[data-testid="confirmation-confirm"]')].at(-1)
    expect(confirm).toBeTruthy()
    confirm!.click()
    await flushPromises()
    expect(wrapper.text()).toContain('Type a prompt to start.')
    wrapper.unmount()
  })

  it('adopts multimodal image content from raw JSON requests', async () => {
    mocks.request.mockResolvedValue({
      request: { instance_id: 'coder', status_code: 200, result: 'success', duration_ms: 200, prompt_tokens: 0, generated_tokens: 0, total_tokens: 0, load_duration_ms: 0, autoloaded: false },
      state_trace: ['READY'], evictions_triggered: []
    })
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ choices: [{ message: { content: 'vision reply' } }] }), {
      status: 200,
      headers: { 'X-LlamaRack-Request-ID': 'raw-image' }
    })))

    const wrapper = await mountSuspended(PlaygroundPage, { route: '/playground' })
    await prepareRaw(wrapper, {
      model: 'coder',
      messages: [{
        role: 'user',
        content: [
          { type: 'text', text: 'describe this' },
          { type: 'image_url', image_url: { url: 'data:image/png;base64,abc' } }
        ]
      }],
      stream: false
    })
    await sendPlayground(wrapper)
    await flushPromises()

    expect(wrapper.text()).toContain('describe this')
    expect(wrapper.text()).toContain('vision reply')
    wrapper.unmount()
  })

  it('renders empty-instance and runtime-state variants without inventing a target', async () => {
    mocks.manager.instances.value = []
    mocks.manager.models.value = []
    let wrapper = await mountSuspended(PlaygroundPage, { route: '/playground' })
    await flushPromises()
    expect(wrapper.text()).toContain('Select an Instance')
    expect(promptSubmit(wrapper).attributes('disabled')).toBeDefined()
    wrapper.unmount()

    for (const state of ['FAILED', 'STARTING', 'LOADING', 'DRAINING', 'STOPPING', 'UNLOADED']) {
      resetManager()
      mocks.runtime.state = state
      wrapper = await mountSuspended(PlaygroundPage, { route: '/playground' })
      await flushPromises()
      expect(wrapper.text()).toContain(state)
      wrapper.unmount()
    }
  })
})
