import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import PlaygroundPage from '~/pages/playground.vue'
import {
  livePlaygroundTurnStats,
  mergePlaygroundTurnStats,
  playgroundTurnStatsFromDiagnostics
} from '~/utils/playgroundTurnStats'

const mocks = vi.hoisted(() => ({
  request: vi.fn(),
  runtime: { instance_id: 'coder', model_id: 'model-1', state: 'READY', pid: 77, port: 9101 },
  manager: null as any
}))

mocks.manager = {
  apiBase: { value: 'http://manager.test:8888' },
  instances: { value: [
    {
      id: '550e8400-e29b-41d4-a716-446655440000', slug: 'coder', model_id: 'model-1', name: 'Coder', enabled: true, autoload_enabled: true, always_on: false,
      priority: 'normal', eviction_enabled: true, idle_unload_seconds: 300, gpu_mode: 'auto'
    },
    {
      id: '550e8400-e29b-41d4-a716-446655440001', slug: 'other', model_id: 'model-2', name: 'Other', enabled: true, autoload_enabled: true, always_on: false,
      priority: 'normal', eviction_enabled: true, idle_unload_seconds: 300, gpu_mode: 'auto'
    }
  ] },
  models: { value: [
    { id: 'model-1', name: 'Qwen Coder', gguf_path: 'qwen.gguf', total_bytes: 1, context_length: 32768 },
    { id: 'model-2', name: 'Other Model', gguf_path: 'other.gguf', total_bytes: 1, context_length: 4096 }
  ] },
  runtimeForInstance: vi.fn(() => mocks.runtime),
  telemetryForInstance: vi.fn(() => undefined),
  instanceState: vi.fn(() => 'READY'),
  request: mocks.request
}

mockNuxtImport('useManager', () => () => mocks.manager)

function diagnostic(requestID: string, generatedTokens: number) {
  return {
    request: {
      request_id: requestID,
      instance_id: 'coder',
      status_code: 200,
      result: 'success',
      duration_ms: 900,
      ttft_ms: 150,
      prompt_tokens: 12,
      generated_tokens: generatedTokens,
      total_tokens: 12 + generatedTokens,
      generation_tokens_per_second: 20,
      load_duration_ms: 0,
      autoloaded: false
    },
    inference_stats: { prompt_n: 12, predicted_n: generatedTokens, predicted_ms: 500, predicted_per_second: 20 },
    state_trace: ['READY'],
    evictions_triggered: []
  }
}

async function send(wrapper: any, text: string) {
  await wrapper.get('textarea[aria-label="Playground message"]').setValue(text)
  await wrapper.get('[data-testid="playground-prompt-submit"]').trigger('click')
  await flushPromises()
}

beforeEach(() => {
  mocks.request.mockReset()
  sessionStorage.clear()
  localStorage.clear()
  sessionStorage.setItem('llamarack_management_token', 'management-playground')
  vi.unstubAllGlobals()
})

describe('Playground CodeRabbit regressions', () => {
  it('keeps regenerated alternatives visible but excludes the full trailing alternative group from model context', async () => {
    mocks.request.mockImplementation(async (path: string) => {
      const requestID = path.split('/').at(-1) || 'req-1'
      const turn = Number(requestID.split('-').at(-1)) || 1
      return diagnostic(requestID, turn * 4)
    })

    let call = 0
    const publicFetch = vi.fn(async () => {
      call += 1
      return new Response(`data: {"choices":[{"delta":{"content":"reply ${call}"},"finish_reason":"stop"}]}\n\ndata: [DONE]\n\n`, {
        status: 200,
        headers: { 'Content-Type': 'text/event-stream', 'X-LlamaRack-Request-ID': `req-${call}` }
      })
    })
    vi.stubGlobal('fetch', publicFetch)

    const wrapper = await mountSuspended(PlaygroundPage, { route: '/playground' })
    await flushPromises()

    await send(wrapper, 'original prompt')
    await wrapper.get('[data-testid="playground-regenerate"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-testid="playground-regenerate"]').trigger('click')
    await flushPromises()
    await send(wrapper, 'follow up')

    expect(publicFetch).toHaveBeenCalledTimes(4)
    const secondRegenerateBody = JSON.parse(String(publicFetch.mock.calls[2]![1].body))
    expect(secondRegenerateBody.messages).toEqual([
      { role: 'user', content: 'original prompt' }
    ])

    const followUpBody = JSON.parse(String(publicFetch.mock.calls[3]![1].body))
    expect(followUpBody.messages).toEqual([
      { role: 'user', content: 'original prompt' },
      { role: 'assistant', content: 'reply 3' },
      { role: 'user', content: 'follow up' }
    ])

    const assistantTexts = wrapper.findAll('[data-testid="playground-assistant-text"]').map(item => item.text())
    expect(assistantTexts).toEqual(expect.arrayContaining(['reply 1', 'reply 2', 'reply 3', 'reply 4']))
    expect(wrapper.findAll('[data-testid="playground-turn-stats-summary"]').length).toBe(4)
    wrapper.unmount()
  })

  it('keeps diagnostics context usage bound to the assistant turn after switching instances', async () => {
    mocks.request.mockImplementation(async (path: string) => diagnostic(path.split('/').at(-1) || 'req-1', 4))
    const publicFetch = vi.fn(async () => new Response('data: {"choices":[{"delta":{"content":"reply"},"finish_reason":"stop"}]}\n\ndata: [DONE]\n\n', {
      status: 200,
      headers: { 'Content-Type': 'text/event-stream', 'X-LlamaRack-Request-ID': 'req-1' }
    }))
    vi.stubGlobal('fetch', publicFetch)

    const wrapper = await mountSuspended(PlaygroundPage, { route: '/playground' })
    await flushPromises()
    await send(wrapper, 'original prompt')

    expect(wrapper.get('[data-testid="playground-diagnostics"]').text()).toContain('16 / 32768')
    const instanceButtons = wrapper.findAll('[data-testid="playground-instance-list"] button')
    expect(instanceButtons).toHaveLength(2)
    await instanceButtons[1]!.trigger('click')
    await flushPromises()

    const diagnosticsText = wrapper.get('[data-testid="playground-diagnostics"]').text()
    expect(diagnosticsText).toContain('16 / 32768')
    expect(diagnosticsText).not.toContain('16 / 4096')
    wrapper.unmount()
  })

  it('keeps browser TTFT estimated until diagnostics provide an authoritative TTFT', () => {
    const live = livePlaygroundTurnStats(1000, 1250, 1800)
    const sparseFinal = playgroundTurnStatsFromDiagnostics({
      request: { duration_ms: 750, prompt_tokens: 4, generated_tokens: 5, total_tokens: 9 }
    })
    const merged = mergePlaygroundTurnStats(live, sparseFinal)

    expect(merged).toMatchObject({
      wallMs: 750,
      wallEstimated: false,
      ttftMs: 250,
      ttftEstimated: true
    })
  })
})
