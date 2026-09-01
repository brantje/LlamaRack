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

function button(wrapper: any, text: string) {
  const found = wrapper.findAll('button').find((candidate: any) => candidate.text().trim() === text)
  if (!found) throw new Error(`Missing button: ${text}`)
  return found
}

function selectedTab(wrapper: any) {
  return wrapper.findAll('[role="tab"]').find((tab: any) => tab.attributes('aria-selected') === 'true')
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
  sessionStorage.setItem('llamarack_management_token', 'management-playground')
  vi.unstubAllGlobals()
})

describe('Playground UX', () => {
  it('streams assistant tokens into the chat thread without leaving the Parameters tab', async () => {
    const sse = createSSEStream()
    vi.stubGlobal('fetch', vi.fn(async () => new Response(sse.stream, {
      status: 200,
      headers: { 'X-LlamaRack-Request-ID': 'req-ux' }
    })))

    const wrapper = await mountSuspended(PlaygroundPage, { route: '/playground' })
    await flushPromises()
    expect(wrapper.get('[data-testid="playground-model-name"]').text()).toBe('Qwen Coder')

    await wrapper.get('textarea[aria-label="Playground message"]').setValue('stream please')
    await sendPlayground(wrapper)
    await flushPromises()

    expect(wrapper.get('[data-testid="playground-parameters"]').exists()).toBe(true)
    expect(selectedTab(wrapper)?.text()).toBe('Parameters')
    expect(wrapper.get('[data-testid="playground-chat-messages"]').text()).toContain(PLAYGROUND_GENERATING_PLACEHOLDER)
    expect(wrapper.get('[data-testid="playground-phase-label"]').text()).toBe(PLAYGROUND_GENERATING_PLACEHOLDER)
    expect(wrapper.get('[data-testid="playground-chat-indicator"]').text()).toBe(PLAYGROUND_GENERATING_PLACEHOLDER)
    expect(wrapper.findAll('[data-testid="playground-phase-label"]')).toHaveLength(1)
    expect(wrapper.get('[data-testid="playground-chat-messages"]').text()).not.toContain('Exercise an Instance')
    const assistantPlaceholder = wrapper.find('[data-testid="playground-assistant-text"]')
    if (assistantPlaceholder.exists()) {
      expect(assistantPlaceholder.text()).not.toContain(PLAYGROUND_GENERATING_PLACEHOLDER)
    }

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
    expect(selectedTab(wrapper)?.text()).toBe('Parameters')
    wrapper.unmount()
  })

  it('shows reasoning UI, empty-content fallback and truncation warning for reasoning-only SSE', async () => {
    const sse = createSSEStream()
    vi.stubGlobal('fetch', vi.fn(async () => new Response(sse.stream, {
      status: 200,
      headers: { 'X-LlamaRack-Request-ID': 'req-reason' }
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
    await wrapper.get('[data-testid="playground-copy-message"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-testid="playground-notice"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('renders reasoning above assistant text even when text streams first', async () => {
    const sse = createSSEStream()
    vi.stubGlobal('fetch', vi.fn(async () => new Response(sse.stream, {
      status: 200,
      headers: { 'X-LlamaRack-Request-ID': 'req-order' }
    })))

    const wrapper = await mountSuspended(PlaygroundPage, { route: '/playground' })
    await flushPromises()
    await wrapper.get('textarea[aria-label="Playground message"]').setValue('order check')
    await sendPlayground(wrapper)
    await flushPromises()

    sse.push('data: {"choices":[{"delta":{"content":"Final answer."}}]}\n\n')
    await flushPromises()
    sse.push('data: {"choices":[{"delta":{"reasoning_content":"Late thought."}}]}\n\n')
    await flushPromises()

    const reasoning = wrapper.get('[data-testid="playground-reasoning"]')
    const text = wrapper.get('[data-testid="playground-assistant-text"]')
    expect(reasoning.text()).toContain('Late thought.')
    expect(text.text()).toContain('Final answer.')
    expect(reasoning.element.compareDocumentPosition(text.element) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    wrapper.unmount()
  })

  it('shows an empty-content fallback when a completed stream has no text or reasoning', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(
      'data: {"choices":[{"delta":{},"finish_reason":"stop"}]}\n\ndata: [DONE]\n\n',
      { status: 200, headers: { 'X-LlamaRack-Request-ID': 'req-empty' } }
    )))

    const wrapper = await mountSuspended(PlaygroundPage, { route: '/playground' })
    await flushPromises()
    await wrapper.get('textarea[aria-label="Playground message"]').setValue('empty please')
    await sendPlayground(wrapper)
    await flushPromises()
    expect(wrapper.get('[data-testid="playground-empty-content"]').text()).toContain('no visible text')
    expect(wrapper.find('[data-testid="playground-truncation-warning"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('confirms clear conversation in a modal and leaves state untouched on cancel', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(
      'data: {"choices":[{"delta":{"content":"keep me"}}]}\n\ndata: [DONE]\n\n',
      { status: 200, headers: { 'X-LlamaRack-Request-ID': 'req-clear' } }
    )))

    const wrapper = await mountSuspended(PlaygroundPage, { route: '/playground' })
    await flushPromises()
    const firstSession = wrapper.get('[data-testid="playground-session-id"]').text().trim()
    expect(firstSession.length).toBeGreaterThan(8)
    await wrapper.get('textarea[aria-label="Playground message"]').setValue('remember this')
    await sendPlayground(wrapper)
    await flushPromises()
    expect(wrapper.get('[data-testid="playground-chat-messages"]').text()).toContain('keep me')

    await wrapper.get('[data-testid="playground-clear-conversation"]').trigger('click')
    await flushPromises()
    expect(document.body.textContent).toContain('This removes all messages, diagnostics, and the captured raw request/response')
    expect(confirmationButton('confirm').textContent).toContain('Clear chat')

    confirmationButton('cancel').click()
    await flushPromises()
    expect(wrapper.get('[data-testid="playground-chat-messages"]').text()).toContain('keep me')
    expect(wrapper.get('[data-testid="playground-chat-messages"]').text()).toContain('remember this')

    await wrapper.get('[data-testid="playground-clear-conversation"]').trigger('click')
    await flushPromises()
    confirmationButton('confirm').click()
    await flushPromises()
    expect(wrapper.text()).toContain('Type a prompt to start.')
    expect(wrapper.text()).not.toContain('Exercise an Instance through the real gateway.')
    expect(wrapper.find('[data-testid="playground-chat-messages"]').exists()).toBe(false)
    const nextSession = wrapper.get('[data-testid="playground-session-id"]').text().trim()
    expect(nextSession).not.toBe(firstSession)
    wrapper.unmount()
  })

  it('switches to the Response tab only after an HTTP error', async () => {
    vi.stubGlobal('fetch', vi.fn()
      .mockResolvedValueOnce(new Response(
        'data: {"choices":[{"delta":{"content":"ok"}}]}\n\ndata: [DONE]\n\n',
        { status: 200, headers: { 'X-LlamaRack-Request-ID': 'req-ok' } }
      ))
      .mockResolvedValueOnce(new Response(JSON.stringify({ error: { message: 'upstream rejected' } }), { status: 400 }))
    )

    const wrapper = await mountSuspended(PlaygroundPage, { route: '/playground' })
    await flushPromises()
    await wrapper.get('textarea[aria-label="Playground message"]').setValue('first turn')
    await sendPlayground(wrapper)
    await flushPromises()
    expect(wrapper.get('[data-testid="playground-parameters"]').exists()).toBe(true)
    expect(selectedTab(wrapper)?.text()).toBe('Parameters')

    await wrapper.get('textarea[aria-label="Playground message"]').setValue('retry after error')
    await sendPlayground(wrapper)
    await flushPromises()
    expect(wrapper.get('[data-testid="playground-response"]').exists()).toBe(true)
    const requestError = wrapper.get('[data-testid="playground-error"]')
    expect(requestError.text()).toContain('Request error')
    expect(requestError.text()).toContain('upstream rejected')
    expect(requestError.element.querySelectorAll(':scope > .flex.w-full')).toHaveLength(0)
    expect(requestError.get('p').text()).toBe('upstream rejected')
    expect(wrapper.find('[data-testid="playground-empty-content"]').exists()).toBe(false)
    expect(selectedTab(wrapper)?.text()).toBe('Response')
    expect(selectedTab(wrapper)?.attributes('aria-controls')).toBeTruthy()
    wrapper.unmount()
  })

  it('uses UFormField labels on parameters and high-contrast user bubbles', async () => {
    const wrapper = await mountSuspended(PlaygroundPage, { route: '/playground' })
    await flushPromises()
    const parameters = wrapper.get('[data-testid="playground-parameters"]')
    expect(parameters.findAll('label').length).toBeGreaterThanOrEqual(8)
    expect(parameters.text()).toContain('temperature')
    expect(parameters.text()).toContain('system prompt')
    expect(wrapper.get('input[placeholder="token, or one per line"]').exists()).toBe(true)
    wrapper.unmount()
  })

  it('keeps the composer in the thread column while a tall request panel lives in the independent rail', async () => {
    const wrapper = await mountSuspended(PlaygroundPage, { route: '/playground' })
    await flushPromises()
    const page = wrapper.get('[data-testid="playground-page"]')
    expect(page.classes()).toContain('overflow-hidden')
    const thread = wrapper.get('[data-testid="playground-thread"]')
    expect(thread.classes()).toContain('overflow-hidden')
    const composer = wrapper.get('[data-testid="playground-composer"]')
    expect(composer.classes()).toContain('shrink-0')
    expect(thread.element.contains(composer.element)).toBe(true)
    const rail = wrapper.get('[data-testid="playground-rail"]')
    expect(rail.classes().some((value: string) => value.includes('overflow-y-auto'))).toBe(true)
    await activateTab(wrapper, 'Request')
    await flushPromises()
    expect(wrapper.get('[data-testid="playground-request"]').exists()).toBe(true)
    expect(wrapper.get('[data-testid="playground-composer"]').exists()).toBe(true)
    expect(thread.element.contains(wrapper.get('[data-testid="playground-composer"]').element)).toBe(true)
    wrapper.unmount()
  })

  it('disables send when the composer is empty and enables it for text or attachments', async () => {
    vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:preview')
    vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {})
    const wrapper = await mountSuspended(PlaygroundPage, { route: '/playground' })
    await flushPromises()
    expect(promptSubmit(wrapper).attributes('disabled')).toBeDefined()
    expect(promptSubmit(wrapper).attributes('aria-label')).toBe('Send prompt')

    await wrapper.get('textarea[aria-label="Playground message"]').setValue('hello')
    await flushPromises()
    expect(promptSubmit(wrapper).attributes('disabled')).toBeUndefined()

    await wrapper.get('textarea[aria-label="Playground message"]').setValue('   ')
    await flushPromises()
    expect(promptSubmit(wrapper).attributes('disabled')).toBeDefined()

    const file = new File(['fake'], 'diagram.png', { type: 'image/png' })
    const input = wrapper.get('[data-testid="playground-file-input"]')
    Object.defineProperty(input.element, 'files', { value: [file] })
    await input.trigger('change')
    await flushPromises()
    expect(promptSubmit(wrapper).attributes('disabled')).toBeUndefined()
    wrapper.unmount()
  })

  it('labels copy failures as a notice instead of a request error', async () => {
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: { writeText: vi.fn().mockRejectedValue(new Error('denied')) }
    })
    const wrapper = await mountSuspended(PlaygroundPage, { route: '/playground' })
    await flushPromises()
    await button(wrapper, 'Copy as curl').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="playground-notice"]').text()).toContain('Unable to copy curl.')
    expect(wrapper.find('[data-testid="playground-error"]').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('Request error')
    wrapper.unmount()
  })

  it('copies an assistant message and regenerates the last turn', async () => {
    const publicFetch = vi.fn(async () => new Response(
      'data: {"choices":[{"delta":{"content":"first reply"}}]}\n\ndata: [DONE]\n\n',
      { status: 200, headers: { 'X-LlamaRack-Request-ID': 'req-regen' } }
    ))
    vi.stubGlobal('fetch', publicFetch)
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: { writeText: vi.fn().mockResolvedValue(undefined) }
    })
    const wrapper = await mountSuspended(PlaygroundPage, { route: '/playground' })
    await flushPromises()
    await wrapper.get('textarea[aria-label="Playground message"]').setValue('again please')
    await sendPlayground(wrapper)
    await flushPromises()
    expect(wrapper.get('[data-testid="playground-assistant-text"]').text()).toContain('first reply')

    await wrapper.get('[data-testid="playground-copy-message"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="playground-notice"]').text()).toBe('Message copied.')

    await wrapper.get('[data-testid="playground-regenerate"]').trigger('click')
    await flushPromises()
    expect(publicFetch).toHaveBeenCalledTimes(2)
    wrapper.unmount()
  })

  it('keeps assistant message ids unique after rebuilt conversation turns', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(
      'data: {"choices":[{"delta":{"content":"next"},"finish_reason":"stop"}]}\n\ndata: [DONE]\n\n',
      { status: 200, headers: { 'X-LlamaRack-Request-ID': 'req-ids' } }
    )))
    const wrapper = await mountSuspended(PlaygroundPage, { route: '/playground' })
    await flushPromises()
    ;(wrapper.vm as any).adoptBody({
      model: 'coder',
      messages: [
        { role: 'user', content: 'hi' },
        { role: 'assistant', content: 'hello' }
      ]
    })
    await flushPromises()
    await wrapper.get('textarea[aria-label="Playground message"]').setValue('again')
    await sendPlayground(wrapper)
    await flushPromises()
    const ids = ((wrapper.vm as any).conversation as Array<{ id: string }>).map(message => message.id)
    expect(ids.length).toBeGreaterThanOrEqual(3)
    expect(new Set(ids).size).toBe(ids.length)
    wrapper.unmount()
  })
})
