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

  it('marks only browser wall and first-token timing as approximate and lets final data override it', () => {
    const live = livePlaygroundTurnStats(1000, 1250, 1800)
    expect(live).toEqual({ wallMs: 800, wallEstimated: true, ttftMs: 250, ttftEstimated: true })

    const final = mergePlaygroundTurnStats(live, { wallMs: 750, wallEstimated: false, ttftMs: 200, ttftEstimated: false, generatedTokens: 8 })
    expect(final).toMatchObject({ wallMs: 750, wallEstimated: false, ttftMs: 200, ttftEstimated: false, generatedTokens: 8 })
  })
})
