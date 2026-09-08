import { flushPromises } from '@vue/test-utils'
import { mountSuspended } from '@nuxt/test-utils/runtime'
import { describe, expect, it } from 'vitest'
import TurnStats from '~/components/playground/TurnStats.vue'

describe('Playground TurnStats presentation', () => {
  it('renders approximate timings and sparse zero-valued detail metrics', async () => {
    const wrapper = await mountSuspended(TurnStats, {
      props: {
        stats: {
          generationRate: 12.345,
          generatedTokens: 0,
          ttftMs: 1200,
          ttftEstimated: true,
          wallMs: 900,
          wallEstimated: true,
          promptTokens: 0,
          promptRate: 20,
          promptMs: 0,
          promptPerTokenMs: 0,
          generationMs: 0,
          generationPerTokenMs: 0,
          queueMs: 0,
          cacheTokens: 0,
          cachePercent: 0,
          draftAccepted: 2,
          contextUsed: 9,
          finishReason: 'length',
          toolCallCount: 0
        }
      }
    })

    expect(wrapper.get('[data-testid="playground-turn-stats-summary"]').text()).toBe(
      '12.35 tok/s · 0 generated · ~1.20 s TTFT · ~900 ms wall'
    )

    await wrapper.get('[data-testid="playground-turn-stats-summary"]').trigger('click')
    await flushPromises()

    const details = wrapper.get('[data-testid="playground-turn-stats-details"]').text()
    expect(details).toContain('Prompt0 tokens · 20.00 tok/s · 0 ms · 0.00 ms/token')
    expect(details).toContain('Generation0 tokens · 12.35 tok/s · 0 ms · 0.00 ms/token')
    expect(details).toContain('Queue0 ms')
    expect(details).toContain('Cache0 reused · 0.0%')
    expect(details).toContain('Draft2 / — accepted')
    expect(details).toContain('Context9')
    expect(details).toContain('Stoplength')
    expect(details).toContain('Tool calls0')
  })

  it('renders the complementary sparse draft/context branches and hides empty stats', async () => {
    const wrapper = await mountSuspended(TurnStats, {
      props: {
        stats: {
          wallMs: 0,
          draftProposed: 4,
          contextUsed: 5,
          contextMax: 10,
          contextPercent: 50
        }
      }
    })

    await wrapper.get('[data-testid="playground-turn-stats-summary"]').trigger('click')
    await flushPromises()

    const details = wrapper.get('[data-testid="playground-turn-stats-details"]').text()
    expect(details).toContain('Draft— / 4 accepted')
    expect(details).toContain('Context5 / 10 · 50.0%')

    const empty = await mountSuspended(TurnStats, { props: { stats: {} } })
    expect(empty.find('[data-testid="playground-turn-stats"]').exists()).toBe(false)
  })
})
