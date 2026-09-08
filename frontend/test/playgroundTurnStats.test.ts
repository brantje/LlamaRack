import { describe, expect, it } from 'vitest'
import {
  livePlaygroundTurnStats,
  mergePlaygroundTurnStats,
  parsePlaygroundSSETurnStats,
  playgroundTurnStatsFromDiagnostics,
  playgroundTurnStatsFromResponsePayload
} from '~/utils/playgroundTurnStats'

describe('playgroundTurnStats', () => {
  it('prefers authoritative llama timings and derives cache, draft and context percentages', () => {
    const stats = playgroundTurnStatsFromDiagnostics({
      request: {
        duration_ms: 900,
        ttft_ms: 150,
        prompt_tokens: 99,
        generated_tokens: 88,
        total_tokens: 187,
        tokens_per_second: 32,
        prompt_tokens_per_second: 700,
        generation_tokens_per_second: 40,
        queue_duration_ms: 45
      },
      inference_stats: {
        prompt_n: 12,
        prompt_ms: 15,
        prompt_per_second: 800,
        prompt_per_token_ms: 1.25,
        predicted_n: 24,
        predicted_ms: 500,
        predicted_per_second: 48,
        predicted_per_token_ms: 20.833,
        cache_n: 4,
        draft_n: 10,
        draft_n_accepted: 8,
        finish_reason: 'stop',
        tool_call_count: 2
      }
    }, 32768)

    expect(stats).toMatchObject({
      promptTokens: 12,
      generatedTokens: 24,
      promptRate: 800,
      generationRate: 48,
      promptMs: 15,
      generationMs: 500,
      cacheTokens: 4,
      cachePercent: 25,
      draftAcceptancePercent: 80,
      contextUsed: 40,
      contextMax: 32768,
      finishReason: 'stop',
      toolCallCount: 2,
      queueMs: 45,
      wallMs: 900,
      ttftMs: 150,
      wallEstimated: false,
      ttftEstimated: false
    })
    expect(stats.contextPercent).toBeCloseTo((40 / 32768) * 100)
  })

  it('does not invent generation duration or unavailable zero-valued timing fields', () => {
    const stats = playgroundTurnStatsFromDiagnostics({
      request: { duration_ms: 900, ttft_ms: 150, prompt_tokens: 12, generated_tokens: 24, total_tokens: 36 }
    }, 32768, 'length')

    expect(stats.generationMs).toBeUndefined()
    expect(stats.cacheTokens).toBeUndefined()
    expect(stats.draftProposed).toBeUndefined()
    expect(stats.contextUsed).toBe(36)
    expect(stats.finishReason).toBe('length')
  })

  it('preserves authoritative zero cache and speculative counters', () => {
    const stats = playgroundTurnStatsFromDiagnostics({
      request: {},
      inference_stats: { prompt_n: 4, predicted_n: 5, cache_n: 0, draft_n: 0, draft_n_accepted: 0 }
    })

    expect(stats.cacheTokens).toBe(0)
    expect(stats.cachePercent).toBe(0)
    expect(stats.draftProposed).toBe(0)
    expect(stats.draftAccepted).toBe(0)
    expect(stats.draftAcceptancePercent).toBeUndefined()
    expect(stats.contextUsed).toBe(9)
  })

  it('uses request fallbacks and derives per-token durations only from real telemetry', () => {
    const stats = playgroundTurnStatsFromDiagnostics({
      request: {
        prompt_tokens: 4,
        generated_tokens: 5,
        prompt_tokens_per_second: 20,
        generation_tokens_per_second: 25,
        tokens_per_second: 30,
        queue_duration_ms: -1,
        duration_ms: Number.NaN,
        ttft_ms: -5
      },
      inference_stats: {
        prompt_ms: 8,
        predicted_ms: 20,
        finish_reason: '   ',
        tool_call_count: -1
      }
    }, 0, ' stop ')

    expect(stats).toMatchObject({
      promptTokens: 4,
      generatedTokens: 5,
      promptRate: 20,
      generationRate: 25,
      promptMs: 8,
      promptPerTokenMs: 2,
      generationMs: 20,
      generationPerTokenMs: 4,
      finishReason: 'stop'
    })
    expect(stats.queueMs).toBeUndefined()
    expect(stats.wallMs).toBeUndefined()
    expect(stats.ttftMs).toBeUndefined()
    expect(stats.contextMax).toBeUndefined()
    expect(stats.toolCallCount).toBeUndefined()

    const legacyRate = playgroundTurnStatsFromDiagnostics({
      request: { generated_tokens: 2, tokens_per_second: 12 }
    })
    expect(legacyRate.generationRate).toBe(12)
  })

  it('handles absent diagnostics sections and invalid numeric values without inventing metrics', () => {
    const empty = playgroundTurnStatsFromDiagnostics({ request: null as any, inference_stats: undefined }, undefined, '   ')
    expect(empty.promptTokens).toBeUndefined()
    expect(empty.generatedTokens).toBeUndefined()
    expect(empty.contextUsed).toBeUndefined()
    expect(empty.finishReason).toBeUndefined()

    const invalid = playgroundTurnStatsFromDiagnostics({
      request: { prompt_tokens: 0, generated_tokens: -1, total_tokens: 0 },
      inference_stats: { prompt_n: -1, predicted_n: Number.POSITIVE_INFINITY }
    })
    expect(invalid.promptTokens).toBeUndefined()
    expect(invalid.generatedTokens).toBeUndefined()
    expect(invalid.totalTokens).toBeUndefined()
  })

  it('parses final JSON and SSE timing payloads without estimating tokens from content', () => {
    const payload = {
      usage: { prompt_tokens: 3, completion_tokens: 7, total_tokens: 10 },
      timings: { prompt_n: 3, prompt_ms: 6, predicted_n: 7, predicted_ms: 140, predicted_per_second: 50, cache_n: 2 },
      choices: [{ finish_reason: 'stop' }]
    }
    const direct = playgroundTurnStatsFromResponsePayload(payload, 100)
    const streamed = parsePlaygroundSSETurnStats(`data: ${JSON.stringify(payload)}`, 100)

    expect(direct).toMatchObject({ promptTokens: 3, generatedTokens: 7, generationMs: 140, generationRate: 50, contextUsed: 12, finishReason: 'stop' })
    expect(streamed).toMatchObject(direct!)
    expect(parsePlaygroundSSETurnStats('data: [DONE]')).toBeUndefined()
    expect(parsePlaygroundSSETurnStats('data: {bad')).toBeUndefined()
    expect(playgroundTurnStatsFromResponsePayload({ choices: [{ delta: { content: 'lots of characters' } }] })).toBeUndefined()
  })

  it('parses nested response payloads, alternate usage names and sparse malformed sections', () => {
    const nested = playgroundTurnStatsFromResponsePayload({
      response: {
        usage: { input_tokens: 6, output_tokens: 2, total_tokens: 8 },
        timings: { prompt_n: 6, prompt_ms: 12, predicted_n: 2, predicted_ms: 10, cache_n: -1 },
        choices: [{ finish_reason: '' }, { finish_reason: 'length' }]
      }
    }, 16)
    expect(nested).toMatchObject({
      promptTokens: 6,
      generatedTokens: 2,
      totalTokens: 8,
      promptMs: 12,
      generationMs: 10,
      finishReason: 'length',
      contextUsed: 8,
      contextMax: 16,
      contextPercent: 50
    })
    expect(nested?.cacheTokens).toBeUndefined()

    expect(playgroundTurnStatsFromResponsePayload(null)).toBeUndefined()
    expect(playgroundTurnStatsFromResponsePayload([])).toBeUndefined()
    expect(playgroundTurnStatsFromResponsePayload({ usage: [], timings: [], choices: 'nope' })).toBeUndefined()
    expect(parsePlaygroundSSETurnStats(': keepalive')).toBeUndefined()
    expect(parsePlaygroundSSETurnStats('data:   ')).toBeUndefined()
  })

  it('merges sparse sources without overwriting values with undefined', () => {
    const merged = mergePlaygroundTurnStats(
      undefined,
      { promptTokens: 4, generationRate: 10 },
      { promptTokens: undefined, generationRate: 20, finishReason: 'stop' }
    )
    expect(merged).toEqual({ promptTokens: 4, generationRate: 20, finishReason: 'stop' })
  })

  it('marks only browser wall and first-token timing as approximate and lets final data override it', () => {
    const live = livePlaygroundTurnStats(1000, 1250, 1800)
    expect(live).toEqual({ wallMs: 800, wallEstimated: true, ttftMs: 250, ttftEstimated: true })

    const waiting = livePlaygroundTurnStats(1000, undefined, 900)
    expect(waiting).toEqual({ wallMs: 0, wallEstimated: true, ttftMs: undefined, ttftEstimated: false })

    const clamped = livePlaygroundTurnStats(1000, 900, 900)
    expect(clamped).toEqual({ wallMs: 0, wallEstimated: true, ttftMs: 0, ttftEstimated: true })

    const final = mergePlaygroundTurnStats(live, { wallMs: 750, wallEstimated: false, ttftMs: 200, ttftEstimated: false, generatedTokens: 8 })
    expect(final).toMatchObject({ wallMs: 750, wallEstimated: false, ttftMs: 200, ttftEstimated: false, generatedTokens: 8 })
  })
})
