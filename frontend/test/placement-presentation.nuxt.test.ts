import { describe, expect, it } from 'vitest'
import {
  compactRangeThreshold,
  contextStep,
  contextToSliderPosition,
  currentZoneIndex,
  formatCompactContext,
  formatContextRange,
  gpuAutoRole,
  gpuAutoRoleLabel,
  gpuDisplayName,
  isPlacementRanges,
  minimumContext,
  modelCacheLocations,
  nearbyTransitionCopy,
  noFitSliderMaximum,
  namedDeviceList,
  primaryPlacementResult,
  sliderPositionToContext,
  sliderZones,
  snapContext,
  transitionLabel,
  useCompactRangeLayout,
  usingGpuSummary,
  whyPlacement,
  zoneShortLabel,
  zoneBarLabel,
  type PlacementZone
} from '~/utils/placementPresentation'

function zone(overrides: Partial<PlacementZone> = {}): PlacementZone {
  return {
    start_context: 512,
    end_context: 8192,
    kind: 'gpu',
    offload_mode: 'full',
    gpu_count: 1,
    devices: ['CUDA0'],
    kv_on_gpu: true,
    current_fit: true,
    total_hardware_fit: true,
    ...overrides
  }
}

describe('placementPresentation', () => {
  it('snaps context values to the 512-token step', () => {
    expect(contextStep).toBe(512)
    expect(minimumContext).toBe(512)
    expect(snapContext(65000)).toBe(65024)
    expect(snapContext(65536)).toBe(65536)
    expect(snapContext(512)).toBe(512)
    expect(snapContext(100)).toBe(512)
    expect(snapContext(Number.NaN)).toBe(512)
    expect(snapContext(Number.POSITIVE_INFINITY)).toBe(512)
    expect(snapContext(900, 512, 1024)).toBe(1024)
    expect(snapContext(1000, 512, 400)).toBe(1024)
  })

  it('formats compact context values and range labels', () => {
    expect(formatCompactContext(0)).toBe('0')
    expect(formatCompactContext(512)).toBe('512')
    expect(formatCompactContext(8192)).toBe('8K')
    expect(formatCompactContext(14336)).toBe('14K')
    expect(formatCompactContext(1048576)).toBe('1M')
    expect(formatContextRange(512, 8192)).toBe('512 – 8K')
    expect(zoneShortLabel({ kind: 'gpu', gpu_count: 1 })).toBe('1 GPU')
    expect(zoneShortLabel({ kind: 'gpu', gpu_count: 6 })).toBe('6 GPUs')
    expect(zoneShortLabel({ kind: 'moe', gpu_count: 1 })).toBe('1 GPU + experts in RAM')
    expect(zoneShortLabel({ kind: 'moe', gpu_count: 4 })).toBe('4 GPUs + experts in RAM')
    expect(zoneShortLabel({ kind: 'hybrid', gpu_count: 1 })).toBe('GPU + RAM')
    expect(zoneShortLabel({ kind: 'partial', gpu_count: 1 })).toBe('Partial GPU')
    expect(zoneShortLabel({ kind: 'cpu', gpu_count: 0 })).toBe('CPU')
    expect(zoneShortLabel({ kind: 'no_fit', gpu_count: 0 })).toBe('No fit')
    expect(zoneShortLabel({ kind: 'mystery', gpu_count: 0 })).toBe('mystery')
    expect(zoneBarLabel(zone({ start_context: 512, end_context: 8192 }))).toContain('1 GPU')
  })

  it('selects the current zone and compact layout threshold', () => {
    const zones = [
      zone({ end_context: 8192 }),
      zone({ start_context: 8704, end_context: 16384, kind: 'gpu', gpu_count: 2, offload_mode: 'multi_gpu', devices: ['CUDA0', 'CUDA1'] }),
      zone({ start_context: 16896, end_context: 32768, kind: 'hybrid', offload_mode: 'hybrid', kv_on_gpu: false })
    ]
    expect(currentZoneIndex(zones, 4096)).toBe(0)
    expect(currentZoneIndex(zones, 8193)).toBe(0)
    expect(currentZoneIndex(zones, 8703)).toBe(0)
    expect(currentZoneIndex(zones, 8704)).toBe(1)
    expect(currentZoneIndex(zones, 20000)).toBe(2)
    expect(currentZoneIndex(zones, 100)).toBe(0)
    expect(currentZoneIndex(zones, 999999)).toBe(2)
    expect(currentZoneIndex(undefined, 4096)).toBe(-1)
    const midTwoGpu = contextToSliderPosition(zones, 12000)
    expect(midTwoGpu).toBeGreaterThan(1)
    expect(midTwoGpu).toBeLessThan(2)
    expect(currentZoneIndex(zones, sliderPositionToContext(zones, midTwoGpu))).toBe(1)
    expect(currentZoneIndex(zones, sliderPositionToContext(zones, 2.1))).toBe(2)
    expect(sliderPositionToContext(undefined, 1)).toBe(512)
    expect(contextToSliderPosition(undefined, 4096)).toBe(0)
    const withNoFit = [
      ...zones,
      zone({ start_context: 33280, end_context: 262144, kind: 'no_fit', gpu_count: 0, current_fit: false, total_hardware_fit: false })
    ]
    expect(noFitSliderMaximum(withNoFit)).toBe(33280 + contextStep)
    expect(noFitSliderMaximum(zones)).toBeUndefined()
    expect(sliderZones(withNoFit).at(-1)?.end_context).toBe(33280 + contextStep)
    expect(sliderPositionToContext(sliderZones(withNoFit), sliderZones(withNoFit).length)).toBe(33280 + contextStep)
    expect(useCompactRangeLayout(compactRangeThreshold)).toBe(false)
    expect(useCompactRangeLayout(compactRangeThreshold + 1)).toBe(true)
    expect(transitionLabel(zones[0], zones[1])).toBe('1 → 2 GPUs')
    expect(transitionLabel(zones[1], zones[2])).toBe('2 GPUs → GPU + RAM')
  })

  it('builds beginner placement copy, why text and GPU identity', () => {
    const gpus = [{ id: 'CUDA0', name: 'NVIDIA GeForce RTX 4060 Ti' }, { id: 'CUDA1', name: 'RTX 3090' }]
    expect(gpuDisplayName(gpus, 'CUDA0')).toBe('NVIDIA GeForce RTX 4060 Ti')
    expect(gpuDisplayName(gpus, 'CUDA9')).toBe('CUDA9')
    expect(namedDeviceList(['CUDA0', 'CUDA1'], gpus)).toBe('NVIDIA GeForce RTX 4060 Ti, RTX 3090')
    expect(primaryPlacementResult({ mode: 'full', devices: ['CUDA0'], reason: '' }, true, gpus).title).toBe('Runs on 1 GPU')
    expect(primaryPlacementResult({ mode: 'multi_gpu', devices: ['CUDA0', 'CUDA1', 'CUDA2'], reason: '' }, true).title).toBe('Runs fully on 3 GPUs')
    const moeGPU = primaryPlacementResult({ mode: 'moe', devices: ['CUDA0', 'CUDA1'], n_cpu_moe: 12, kv_on_gpu: true, reason: '' }, true)
    expect(moeGPU.title).toBe('Runs on 2 GPUs + experts in RAM')
    expect(moeGPU.description).toContain('context cache stay on GPU')
    expect(moeGPU.locations).toEqual({ model: 'GPU + expert weights in system RAM', cache: 'GPU' })
    const moeKVInRAM = primaryPlacementResult({ mode: 'moe', devices: ['CUDA0'], n_cpu_moe: 24, kv_on_gpu: false, reason: '' }, true)
    expect(moeKVInRAM.title).toBe('Runs on GPU + experts in RAM')
    expect(moeKVInRAM.description).toContain('context cache use system RAM')
    expect(moeKVInRAM.locations.cache).toBe('system RAM')
    expect(primaryPlacementResult({ mode: 'hybrid', devices: ['CUDA0'], kv_on_gpu: false, reason: '' }, true).title).toBe('Runs on GPU + system memory')
    expect(primaryPlacementResult({ mode: 'partial', devices: ['CUDA0'], kv_on_gpu: true, reason: '' }, true).title).toBe('Runs partly on GPU')
    expect(primaryPlacementResult({ mode: 'cpu', reason: '' }, false, undefined, true).title).toBe('Runs on CPU')
    expect(primaryPlacementResult({ mode: 'cpu', reason: '' }, false).title).toBe('Not enough memory')
    expect(primaryPlacementResult({ mode: 'cpu', reason: '' }, false).variant).toBe('failed')
    expect(modelCacheLocations({ mode: 'hybrid', kv_on_gpu: false, reason: '' })).toEqual({ model: 'GPU', cache: 'system RAM' })
    expect(whyPlacement({ mode: 'multi_gpu', devices: ['a', 'b', 'c'], reason: '' }, true).title).toContain('3 GPUs')
    expect(whyPlacement({ mode: 'multi_gpu', devices: ['a', 'b'], reason: '' }, true).body).toContain('two')
    expect(whyPlacement({ mode: 'full', devices: ['CUDA0'], reason: '' }, true).title).toContain('1 GPU')
    expect(whyPlacement({ mode: 'moe', devices: ['CUDA0', 'CUDA1'], kv_on_gpu: true, reason: '' }, true).title).toContain('experts')
    expect(whyPlacement({ mode: 'moe', devices: ['CUDA0'], kv_on_gpu: false, reason: '' }, true).body).toContain('context cache to RAM')
    expect(whyPlacement({ mode: 'partial', reason: '' }, true).title).toContain('split')
    expect(whyPlacement({ mode: 'cpu', reason: '' }, true).title).toContain('CPU')
    expect(whyPlacement({ mode: 'cpu', reason: '' }, false).title).toContain('enough memory')
    expect(whyPlacement({ mode: 'hybrid', reason: '' }, true).title).toContain('system memory')
    expect(gpuAutoRoleLabel(gpuAutoRole('CUDA0', ['CUDA0']), 1)).toBe('Used by this placement')
    expect(gpuAutoRoleLabel(gpuAutoRole('CUDA1', ['CUDA0', 'CUDA1']), 2)).toBe('Part of 2-GPU placement')
    expect(gpuAutoRoleLabel(gpuAutoRole('CUDA2', ['CUDA0', 'CUDA1']), 2)).toBe('Not needed for this placement')
    expect(usingGpuSummary(6, 8)).toBe('Using 6 of 8 GPUs')
    expect(usingGpuSummary(0, 2)).toBe('')
    expect(isPlacementRanges({ available: false })).toBe(true)
    expect(isPlacementRanges(null)).toBe(false)
  })

  it('explains the previous/next boundary and GPU-only action', () => {
    const ranges = {
      available: true,
      context_step: 512,
      gpu_only_max_context: 14336,
      zones: [
        zone({ start_context: 512, end_context: 14336, kind: 'gpu', gpu_count: 1 }),
        zone({ start_context: 14848, end_context: 262144, kind: 'hybrid', offload_mode: 'hybrid', kv_on_gpu: false })
      ]
    }
    const hybrid = nearbyTransitionCopy(ranges, 14848)
    expect(hybrid.headline).toBe('Uses system memory')
    expect(hybrid.body).toContain('14,336')
    expect(hybrid.actionContext).toBe(14336)
    expect(hybrid.previousLabel).toContain('14K')
    expect(hybrid.previousLabel).toContain('1 GPU → GPU + RAM')
    const near = nearbyTransitionCopy(ranges, 14336)
    expect(near.headline).toBe('Near GPU memory limit')
    expect(near.nextLabel).toContain('14.5K')

    const moeRanges = {
      available: true,
      context_step: 512,
      gpu_only_max_context: 8192,
      zones: [
        zone({ start_context: 512, end_context: 8192, kind: 'gpu', gpu_count: 2, devices: ['CUDA0', 'CUDA1'] }),
        zone({ start_context: 8704, end_context: 32768, kind: 'moe', offload_mode: 'moe', gpu_count: 2, devices: ['CUDA0', 'CUDA1'], n_cpu_moe: 10 })
      ]
    }
    const moe = nearbyTransitionCopy(moeRanges, 8704)
    expect(moe.headline).toBe('Experts use system memory')
    expect(moe.previousLabel).toContain('2 GPUs → 2 GPUs + experts in RAM')
    expect(nearbyTransitionCopy({ available: false }, 4096).current).toBeUndefined()
  })
})
