export type PlacementKind = 'gpu' | 'hybrid' | 'partial' | 'cpu' | 'no_fit'

export type PlacementZone = {
  start_context: number
  end_context: number
  kind: PlacementKind | string
  offload_mode: string
  gpu_count: number
  devices?: string[]
  kv_on_gpu?: boolean
  gpu_layers?: number
  tensor_split?: string
  current_fit: boolean
  total_hardware_fit: boolean
}

export type PlacementRanges = {
  available: boolean
  unavailable_reason?: string
  minimum_context?: number
  maximum_context?: number
  context_step?: number
  gpu_only_max_context?: number
  zones?: PlacementZone[]
}

export type PlacementOffload = {
  mode: string
  gpu_layers?: number
  devices?: string[]
  tensor_split?: string
  kv_on_gpu?: boolean
  reason: string
}

export type PlacementGPU = {
  id: string
  name: string
  total_bytes?: number
}

export type StatusVariant = 'ready' | 'pending' | 'neutral' | 'failed'

export const compactRangeThreshold = 4
export const contextStep = 512
export const minimumContext = 512

export function snapContext(value: number, min = minimumContext, max = Number.POSITIVE_INFINITY) {
  if (!Number.isFinite(value)) return min
  const snapped = Math.round(value / contextStep) * contextStep
  const bounded = Math.max(snapped, min)
  if (!Number.isFinite(max) || max < min) return bounded
  return Math.min(bounded, max)
}

export function formatCompactContext(value: number) {
  if (!Number.isFinite(value) || value <= 0) return '0'
  if (value >= 1024 * 1024 && value % (1024 * 1024) === 0) return `${value / (1024 * 1024)}M`
  if (value >= 1024) {
    const kilo = value / 1024
    return Number.isInteger(kilo) ? `${kilo}K` : `${kilo.toFixed(1)}K`
  }
  return String(value)
}

export function formatContextRange(start: number, end: number) {
  return `${formatCompactContext(start)} – ${formatCompactContext(end)}`
}

export function zoneShortLabel(zone: Pick<PlacementZone, 'kind' | 'gpu_count'>) {
  switch (zone.kind) {
    case 'gpu':
      return zone.gpu_count === 1 ? '1 GPU' : `${zone.gpu_count} GPUs`
    case 'hybrid':
      return 'GPU + RAM'
    case 'partial':
      return 'Partial GPU'
    case 'cpu':
      return 'CPU'
    case 'no_fit':
      return 'No fit'
    default:
      return zone.kind || 'Unknown'
  }
}

export function zoneBarLabel(zone: PlacementZone) {
  return `${zoneShortLabel(zone)}\n${formatContextRange(zone.start_context, zone.end_context)}`
}

export function currentZoneIndex(zones: PlacementZone[] | undefined, context: number) {
  if (!zones?.length) return -1
  let index = 0
  for (let i = 0; i < zones.length; i++) {
    if (context >= zones[i].start_context) index = i
    else break
  }
  return index
}

export function noFitSliderMaximum(zones: PlacementZone[] | undefined, step = contextStep) {
  const noFit = zones?.find(zone => zone.kind === 'no_fit')
  if (!noFit || !Number.isFinite(noFit.start_context) || noFit.start_context <= 0) return undefined
  return noFit.start_context + step
}

export function sliderZones(zones: PlacementZone[] | undefined, step = contextStep) {
  if (!zones?.length) return []
  const max = noFitSliderMaximum(zones, step)
  if (max == null) return zones
  return zones
    .filter(zone => zone.start_context <= max)
    .map(zone => zone.end_context <= max ? zone : { ...zone, end_context: max })
}

export function contextToSliderPosition(zones: PlacementZone[] | undefined, context: number) {
  if (!zones?.length) return 0
  const index = currentZoneIndex(zones, context)
  const zone = zones[index]
  const span = Math.max(1, zone.end_context - zone.start_context)
  const t = Math.min(1, Math.max(0, (context - zone.start_context) / span))
  return index + t
}

export function sliderPositionToContext(zones: PlacementZone[] | undefined, position: number) {
  if (!zones?.length) return minimumContext
  const maxPos = zones.length
  const clamped = Math.min(Math.max(position, 0), maxPos)
  const index = Math.min(zones.length - 1, clamped >= maxPos ? zones.length - 1 : Math.floor(clamped))
  const zone = zones[index]
  const t = clamped >= maxPos ? 1 : clamped - index
  const span = Math.max(0, zone.end_context - zone.start_context)
  return Math.round(zone.start_context + t * span)
}

export function useCompactRangeLayout(zoneCount: number) {
  return zoneCount > compactRangeThreshold
}

export function transitionLabel(from: PlacementZone, to: PlacementZone) {
  if (from.kind === 'gpu' && to.kind === 'gpu') return `${from.gpu_count} → ${to.gpu_count} GPUs`
  return `${zoneShortLabel(from)} → ${zoneShortLabel(to)}`
}

export function gpuDisplayName(gpus: PlacementGPU[] | undefined, deviceId: string) {
  const gpu = gpus?.find(item => item.id === deviceId)
  return gpu?.name?.trim() || deviceId
}

export function namedDeviceList(devices: string[] | undefined, gpus: PlacementGPU[] | undefined) {
  if (!devices?.length) return ''
  return devices.map(id => gpuDisplayName(gpus, id)).join(', ')
}

export function modelCacheLocations(offload: PlacementOffload) {
  if (offload.mode === 'cpu' || !offload.mode) {
    return { model: 'system RAM', cache: 'system RAM' }
  }
  if (offload.mode === 'partial') {
    return { model: 'GPU + system RAM', cache: offload.kv_on_gpu === false ? 'system RAM' : 'GPU' }
  }
  if (offload.mode === 'hybrid') {
    return { model: 'GPU', cache: 'system RAM' }
  }
  return { model: 'GPU', cache: offload.kv_on_gpu === false ? 'system RAM' : 'GPU' }
}

export function primaryPlacementResult(
  offload: PlacementOffload,
  currentFit: boolean,
  gpus?: PlacementGPU[],
  cpuFit = false
) {
  const devices = offload.devices || []
  const gpuCount = devices.length
  const named = namedDeviceList(devices, gpus)
  const locations = modelCacheLocations(offload)
  if (offload.mode === 'full') {
    return {
      title: 'Runs on 1 GPU',
      description: named ? `Everything fits on one ${named}.` : 'Everything fits on one GPU.',
      variant: 'ready' as StatusVariant,
      locations
    }
  }
  if (offload.mode === 'multi_gpu') {
    const count = gpuCount || 2
    return {
      title: `Runs fully on ${count} GPUs`,
      description: `The model and context stay in GPU memory across ${count === 2 ? 'two' : String(count)} GPUs.`,
      variant: 'ready' as StatusVariant,
      locations
    }
  }
  if (offload.mode === 'hybrid') {
    return {
      title: 'Runs on GPU + system memory',
      description: 'The model stays GPU accelerated, but the context cache uses system RAM.',
      variant: 'pending' as StatusVariant,
      locations
    }
  }
  if (offload.mode === 'partial') {
    return {
      title: 'Runs partly on GPU',
      description: 'Some model layers stay on GPU while the remainder use system memory.',
      variant: 'pending' as StatusVariant,
      locations
    }
  }
  if (currentFit || cpuFit) {
    return {
      title: 'Runs on CPU',
      description: 'A useful GPU placement does not fit, but the estimated workload fits system RAM.',
      variant: 'neutral' as StatusVariant,
      locations
    }
  }
  return {
    title: 'Not enough memory',
    description: 'Current available RAM and VRAM are below the conservative estimate.',
    variant: 'neutral' as StatusVariant,
    locations
  }
}

export function whyPlacement(offload: PlacementOffload, currentFit: boolean) {
  const count = offload.devices?.length || 0
  if (offload.mode === 'multi_gpu' && count > 1) {
    return {
      title: `Why are ${count} GPUs being used?`,
      body: `The model no longer fits safely on fewer GPUs, but it fits completely across ${count === 2 ? 'two' : String(count)}.`
    }
  }
  if (offload.mode === 'full') {
    return {
      title: 'Why is 1 GPU being used?',
      body: 'The model and context fit safely on a single GPU, so additional GPUs are not required.'
    }
  }
  if (offload.mode === 'hybrid') {
    return {
      title: 'Why is system memory being used?',
      body: 'At this context size the context cache no longer fits safely in GPU memory.'
    }
  }
  if (offload.mode === 'partial') {
    return {
      title: 'Why is the model split?',
      body: 'The full model no longer fits safely in GPU memory, so some layers stay on GPU while the remainder use system RAM.'
    }
  }
  if (currentFit) {
    return {
      title: 'Why is this running on CPU?',
      body: 'A useful GPU placement does not fit, but the estimated workload fits system RAM.'
    }
  }
  return {
    title: 'Why is there not enough memory?',
    body: 'Current available RAM and VRAM are below the conservative estimate for this context size.'
  }
}

export function nearbyTransitionCopy(ranges: PlacementRanges | undefined, context: number) {
  const zones = ranges?.available ? ranges.zones || [] : []
  const index = currentZoneIndex(zones, context)
  const current = index >= 0 ? zones[index] : undefined
  const previous = index > 0 ? zones[index - 1] : undefined
  const next = index >= 0 && index < zones.length - 1 ? zones[index + 1] : undefined
  const gpuOnlyMax = ranges?.gpu_only_max_context || 0
  const step = ranges?.context_step || 512
  const usesSystem = current?.kind === 'hybrid' || current?.kind === 'partial' || current?.kind === 'cpu' || current?.kind === 'no_fit'
  const actionContext = usesSystem && gpuOnlyMax > 0 && gpuOnlyMax < context ? gpuOnlyMax : 0
  let headline = ''
  let body = ''
  if (actionContext) {
    headline = current?.kind === 'hybrid' || current?.kind === 'partial' ? 'Uses system memory' : current?.kind === 'no_fit' ? 'Not enough memory' : 'Runs on CPU'
    body = `Full GPU placement is available up to ${actionContext.toLocaleString()} tokens.`
  } else if (next && next.start_context === context + step && (next.kind === 'hybrid' || next.kind === 'partial' || next.kind === 'cpu' || next.kind === 'no_fit')) {
    headline = 'Near GPU memory limit'
    body = 'The next 512-token step will require system memory.'
  }
  return {
    current,
    previous,
    next,
    gpuOnlyMax: gpuOnlyMax || undefined,
    previousLabel: previous && current ? `${formatCompactContext(previous.end_context)} — ${transitionLabel(previous, current)}` : '',
    nextLabel: next && current ? `${formatCompactContext(next.start_context)} — ${transitionLabel(current, next)}` : '',
    headline,
    body,
    actionContext: actionContext || undefined
  }
}

export function gpuAutoRole(gpuId: string, devices: string[] | undefined) {
  const selected = devices || []
  if (!selected.length) return 'unused' as const
  if (!selected.includes(gpuId)) return 'unused' as const
  if (selected.length === 1) return 'used' as const
  return 'part' as const
}

export function gpuAutoRoleLabel(role: ReturnType<typeof gpuAutoRole>, gpuCount: number) {
  if (role === 'used') return 'Used by this placement'
  if (role === 'part') return `Part of ${gpuCount}-GPU placement`
  return 'Not needed for this placement'
}

export function usingGpuSummary(deviceCount: number, totalCount: number) {
  if (deviceCount <= 0 || totalCount <= 0) return ''
  return `Using ${deviceCount} of ${totalCount} GPUs`
}

export function isPlacementRanges(value: unknown): value is PlacementRanges {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return false
  const item = value as Partial<PlacementRanges>
  return typeof item.available === 'boolean'
}
