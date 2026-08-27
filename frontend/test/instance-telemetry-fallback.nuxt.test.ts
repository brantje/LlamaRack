import { describe, expect, it } from 'vitest'
import { mountSuspended } from '@nuxt/test-utils/runtime'
import InstanceRuntimeTelemetry from '~/components/InstanceRuntimeTelemetry.vue'

const gib = 1024 ** 3

describe('Instance telemetry fallback labels', () => {
  it('marks unattributed global GPU values instead of presenting them as Instance-scoped', async () => {
    const wrapper = await mountSuspended(InstanceRuntimeTelemetry, {
      route: false,
      props: {
        state: 'READY',
        telemetry: {
          instance_id: 'gemma-4',
          pid: 1652,
          gpu_devices: [],
          gpus: [],
          gpu_utilization_pct: 87,
          vram_used_bytes: 13.9 * gib,
          cpu_percent: 101,
          memory_used_bytes: 1.5 * gib,
          collected_at: '2026-08-27T16:00:00Z'
        }
      }
    })

    expect(wrapper.get('[data-testid="instance-gpu-placement"]').text()).toBe('No GPU allocation detected')
    expect(wrapper.get('[data-testid="instance-gpu-usage"]').text()).toBe('87%')
    expect(wrapper.text()).toContain('GPU usage (global fallback)')
    expect(wrapper.text()).toContain('VRAM (global fallback)')
    expect(wrapper.get('[data-testid="instance-global-fallback"]').text()).toContain('device-wide')
    expect(wrapper.text()).not.toContain('Instance GPU usage')
  })
})
