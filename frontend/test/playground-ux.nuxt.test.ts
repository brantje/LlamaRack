import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import {
  PLAYGROUND_GENERATING_PLACEHOLDER,
  PLAYGROUND_REASONING_ONLY_FALLBACK,
  PLAYGROUND_TRUNCATION_WARNING
} from '~/utils/playgroundChatStream'
import PlaygroundPage from '~/pages/playground.vue'

const mocks = vi.hoisted(() => ({
  request: vi.fn(),
  runtime: { instance_id: 'coder', model_id: 'model-1', state: 'READY', pid: 77, port: 9101 },
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
  telemetryForInstance: vi.fn(() => undefined),
  instanceState: vi.fn(() => mocks.runtime.state),
  request: mocks.request
}

mockNuxtImport('useManager', () => () => mocks.manager)

function promptSubmit(wrapper: any) {
  return wrapper.get('[data-testid="playground-prompt-submit"]')
}

async function sendPlayground(wrapper: any) {
  await promptSubmit(wrapper).trigger('click')
}

function confirmationButton(kind: 'confirm' | 'cancel') {
  const buttons = [...document.body.querySelectorAll<HTMLButtonElement>(`[data-testid="confirmation-${kind}"]`)]
  const match = buttons.at(-1)
  if (!match) throw new Error(`Missing confirmation ${kind} button`)
  return match
}

function createSSEStream() {
  const encoder = new TextEncoder()
  let controller: ReadableStreamDefaultController<Uint8Array>
  const stream = new ReadableStream<Uint8Array>({
    start(current) {
      controller = current
    }
  })
  return {
    stream,
    push(chunk: string) {
      controller.enqueue(encoder.encode(chunk))
    },
    close() {
      controller.close()
    }
  }
}

const diagnostic = {
  request: {
    request_id: 'req-ux', instance_id: 'coder', status_code: 200, result: 'success', duration_ms: 100,
    prompt_tokens: 1, generated_tokens: 1, total_tokens: 2, load_duration_ms: 0, autoloaded: false
  },
  state_trace: ['READY'],
  evictions_triggered: []
}

beforeEach(() => {
  mocks.request.mockReset()
  mocks.request.mockResolvedValue(diagnostic)
  mocks.runtime.state = 'READY'
  sessionStorage.clear()
  localStorage.clear()
  sessionStorage.setItem('lcm_management_token', 'management-playground')
  vi.unstubAllGlobals()
})

describe('Playground UX', () => {
  it('streams assistant tokens into the chat thread without leaving the Parameters tab', async () => {
    const sse = createSSEStream()
    vi.stubGlobal('fetch', vi.fn(async () => new Response(sse.stream, {
      status: 200,
      headers: { 'X-LlamaCPP-Manager-Request-ID': 'req-ux' }
    })))

    const wrapper = await mountSuspended(PlaygroundPage, { route: '/playground' })
    await flushPromises()
    expect(wrapper.get('[data-testid="playground-model-name"]').text()).toBe('Qwen Coder')

    await wrapper.get('textarea[aria-label="Playground message"]').setValue('stream please')
    await sendPlayground(wrapper)
    await flushPromises()

    expect(wrapper.get('[data-testid="playground-parameters"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="playground-response"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="playground-chat-messages"]').text()).toContain(PLAYGROUND_GENERATING_PLACEHOLDER)
    expect(wrapper.get('[data-testid="playground-phase-label"]').text()).toBe('Generating')
    expect(wrapper.get('[data-testid="playground-chat-indicator"]').text()).toBe(PLAYGROUND_GENERATING_PLACEHOLDER)
    expect(wrapper.get('[data-testid="playground-mobile-phase"]').text()).toBe('Generating')

    sse.push('data: {"choices":[{"delta":{"content":"Hello"}}]}\n\n')
    await flushPromises()
    expect(wrapper.get('[data-testid="playground-chat-messages"]').text()).toContain('Hello')
    expect(wrapper.get('[data-testid="playground-assistant-text"]').text()).toContain('Hello')
    expect(wrapper.get('[data-testid="playground-parameters"]').exists()).toBe(true)

    sse.push('data: {"choices":[{"delta":{"content":" world"},"finish_reason":"stop"}]}\n\n')
    sse.close()
    await flushPromises()

    expect(wrapper.get('[data-testid="playground-chat-messages"]').text()).toContain('Hello world')
    expect(wrapper.get('[data-testid="playground-parameters"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="playground-response"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('shows reasoning UI, empty-content fallback and truncation warning for reasoning-only SSE', async () => {
    const sse = createSSEStream()
    vi.stubGlobal('fetch', vi.fn(async () => new Response(sse.stream, {
      status: 200,
      headers: { 'X-LlamaCPP-Manager-Request-ID': 'req-reason' }
    })))

    const wrapper = await mountSuspended(PlaygroundPage, { route: '/playground' })
    await flushPromises()
    await wrapper.get('textarea[aria-label="Playground message"]').setValue('think')
    await sendPlayground(wrapper)
    await flushPromises()

    sse.push('data: {"choices":[{"delta":{"reasoning_content":"Considering the KV cache.","content":null}}]}\n\n')
    await flushPromises()

    const reasoning = wrapper.get('[data-testid="playground-reasoning"]')
    expect(reasoning.text()).toContain('Considering the KV cache.')
    const toggle = reasoning.find('[aria-expanded]')
    expect(toggle.exists()).toBe(true)
    expect(toggle.attributes('aria-expanded')).toBe('true')
    expect(wrapper.get('[data-testid="playground-chat-messages"]').text()).not.toContain(PLAYGROUND_GENERATING_PLACEHOLDER)
    expect(wrapper.get('[data-testid="playground-chat-messages"]').text()).toContain('Considering the KV cache.')

    sse.push('data: {"choices":[{"delta":{},"finish_reason":"length"}]}\n\n')
    sse.close()
    await flushPromises()

    expect(wrapper.get('[data-testid="playground-empty-content"]').text()).toBe(PLAYGROUND_REASONING_ONLY_FALLBACK)
    expect(wrapper.get('[data-testid="playground-truncation-warning"]').text()).toBe(PLAYGROUND_TRUNCATION_WARNING)
    expect(wrapper.get('[data-testid="playground-reasoning"]').find('[aria-expanded]').exists()).toBe(true)
    wrapper.unmount()
  })

  it('shows an empty-content fallback when a completed stream has no text or reasoning', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(
      'data: {"choices":[{"delta":{},"finish_reason":"stop"}]}\n\ndata: [DONE]\n\n',
      { status: 200, headers: { 'X-LlamaCPP-Manager-Request-ID': 'req-empty' } }
    )))

    const wrapper = await mountSuspended(PlaygroundPage, { route: '/playground' })
    await flushPromises()
    await sendPlayground(wrapper)
    await flushPromises()
    expect(wrapper.get('[data-testid="playground-empty-content"]').text()).toContain('no visible text')
    expect(wrapper.find('[data-testid="playground-truncation-warning"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('confirms clear conversation in a modal and leaves state untouched on cancel', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(
      'data: {"choices":[{"delta":{"content":"keep me"}}]}\n\ndata: [DONE]\n\n',
      { status: 200, headers: { 'X-LlamaCPP-Manager-Request-ID': 'req-clear' } }
    )))

    const wrapper = await mountSuspended(PlaygroundPage, { route: '/playground' })
    await flushPromises()
    await wrapper.get('textarea[aria-label="Playground message"]').setValue('remember this')
    await sendPlayground(wrapper)
    await flushPromises()
    expect(wrapper.get('[data-testid="playground-chat-messages"]').text()).toContain('keep me')

    await wrapper.get('[data-testid="playground-clear-conversation"]').trigger('click')
    await flushPromises()
    expect(document.body.textContent).toContain('This removes all messages, diagnostics, and the captured raw request/response')
    expect(confirmationButton('confirm').textContent).toContain('Clear conversation')

    confirmationButton('cancel').click()
    await flushPromises()
    expect(wrapper.get('[data-testid="playground-chat-messages"]').text()).toContain('keep me')
    expect(wrapper.get('[data-testid="playground-chat-messages"]').text()).toContain('remember this')

    await wrapper.get('[data-testid="playground-clear-conversation"]').trigger('click')
    await flushPromises()
    confirmationButton('confirm').click()
    await flushPromises()
    expect(wrapper.text()).toContain('Exercise an Instance through the real gateway.')
    expect(wrapper.find('[data-testid="playground-chat-messages"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('switches to the Response tab only after an HTTP error', async () => {
    vi.stubGlobal('fetch', vi.fn()
      .mockResolvedValueOnce(new Response(
        'data: {"choices":[{"delta":{"content":"ok"}}]}\n\ndata: [DONE]\n\n',
        { status: 200, headers: { 'X-LlamaCPP-Manager-Request-ID': 'req-ok' } }
      ))
      .mockResolvedValueOnce(new Response(JSON.stringify({ error: { message: 'upstream rejected' } }), { status: 400 }))
    )

    const wrapper = await mountSuspended(PlaygroundPage, { route: '/playground' })
    await flushPromises()
    await sendPlayground(wrapper)
    await flushPromises()
    expect(wrapper.get('[data-testid="playground-parameters"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="playground-response"]').exists()).toBe(false)

    await sendPlayground(wrapper)
    await flushPromises()
    expect(wrapper.get('[data-testid="playground-response"]').exists()).toBe(true)
    expect(wrapper.get('[data-testid="playground-error"]').text()).toContain('upstream rejected')
    expect(wrapper.find('[data-testid="playground-empty-content"]').exists()).toBe(false)
    const selectedTab = wrapper.findAll('[role="tab"]').find((tab: any) => tab.attributes('aria-selected') === 'true')
    expect(selectedTab?.text()).toBe('Response')
    wrapper.unmount()
  })

  it('uses UFormField labels on parameters and high-contrast user bubbles', async () => {
    const wrapper = await mountSuspended(PlaygroundPage, { route: '/playground' })
    await flushPromises()
    const parameters = wrapper.get('[data-testid="playground-parameters"]')
    expect(parameters.findAll('label').length).toBeGreaterThanOrEqual(8)
    expect(parameters.text()).toContain('temperature')
    expect(parameters.text()).toContain('system prompt')
    wrapper.unmount()
  })
})
