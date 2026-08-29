<script setup lang="ts">
type ChartPoint = { timestamp: number; value: number | null }
type ChartSeries = { label: string; points: ChartPoint[]; token?: 'accent' | 'accent-strong' | 'neutral' }

const props = withDefaults(defineProps<{
  series: ChartSeries[]
  valueFormat?: 'number' | 'tokens' | 'duration' | 'percent'
  min?: number
  max?: number
}>(), {
  valueFormat: 'number',
  min: 0
})

const width = 320
const height = 142
const left = 36
const right = 10
const top = 10
const bottom = 24
const plotWidth = width - left - right
const plotHeight = height - top - bottom
const allPoints = computed(() => props.series.flatMap(series => series.points))
const timestamps = computed(() => Array.from(new Set(allPoints.value.map(point => point.timestamp))).sort((a, b) => a - b))
const presentValues = computed(() => allPoints.value.flatMap(point => point.value === null || !Number.isFinite(point.value) ? [] : [point.value]))
const domainMin = computed(() => Number.isFinite(props.min) ? props.min : Math.min(0, ...presentValues.value))
const domainMax = computed(() => {
  if (props.max !== undefined && Number.isFinite(props.max)) return props.max
  const maximum = presentValues.value.length ? Math.max(...presentValues.value) : 1
  return maximum <= domainMin.value ? domainMin.value + 1 : maximum * 1.08
})
const gridValues = computed(() => Array.from({ length: 4 }, (_, index) => domainMin.value + (domainMax.value - domainMin.value) * (3 - index) / 3))

function xFor(timestamp: number) {
  const items = timestamps.value
  if (items.length <= 1) return left + plotWidth / 2
  const index = items.indexOf(timestamp)
  return left + Math.max(0, index) / (items.length - 1) * plotWidth
}

function yFor(value: number) {
  const span = domainMax.value - domainMin.value || 1
  const normalized = Math.min(1, Math.max(0, (value - domainMin.value) / span))
  return top + (1 - normalized) * plotHeight
}

function paths(points: ChartPoint[]) {
  const segments: string[] = []
  let current = ''
  for (const point of points) {
    if (point.value === null || !Number.isFinite(point.value)) {
      if (current) segments.push(current)
      current = ''
      continue
    }
    const command = current ? 'L' : 'M'
    current += `${command}${xFor(point.timestamp).toFixed(1)},${yFor(point.value).toFixed(1)} `
  }
  if (current) segments.push(current)
  return segments
}

function strokeClass(token?: ChartSeries['token']) {
  if (token === 'accent-strong') return 'stroke-[var(--accent-800)]'
  if (token === 'neutral') return 'stroke-[var(--neutral-800)]'
  return 'stroke-[var(--color-accent)]'
}

function dotClass(token?: ChartSeries['token']) {
  if (token === 'accent-strong') return 'fill-[var(--accent-800)]'
  if (token === 'neutral') return 'fill-[var(--neutral-800)]'
  return 'fill-[var(--color-accent)]'
}

function formatValue(value: number) {
  if (!Number.isFinite(value)) return '—'
  if (props.valueFormat === 'percent') return `${value.toFixed(1)}%`
  if (props.valueFormat === 'duration') return value < 1000 ? `${Math.round(value)} ms` : `${(value / 1000).toFixed(value >= 10_000 ? 1 : 2)} s`
  if (props.valueFormat === 'tokens') return Math.round(value).toLocaleString()
  return value >= 100 ? Math.round(value).toLocaleString() : value.toFixed(1).replace(/\.0$/, '')
}

function formatTime(timestamp?: number) {
  if (!timestamp) return '—'
  return new Date(timestamp).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
}
</script>

<template>
  <div class="space-y-3">
    <div v-if="series.length > 1" class="flex flex-wrap gap-x-4 gap-y-1 text-[10px] text-[var(--neutral-800)]">
      <span v-for="item in series" :key="item.label" class="inline-flex items-center gap-1.5">
        <span class="h-px w-4" :class="strokeClass(item.token).replace('stroke-', 'bg-')" />{{ item.label }}
      </span>
    </div>
    <svg :viewBox="`0 0 ${width} ${height}`" class="h-40 w-full overflow-visible" role="img" aria-label="History line chart">
      <g v-for="value in gridValues" :key="value">
        <line :x1="left" :x2="width - right" :y1="yFor(value)" :y2="yFor(value)" class="stroke-[var(--color-divider)]" stroke-width="1" vector-effect="non-scaling-stroke" />
        <text :x="left - 5" :y="yFor(value) + 3" text-anchor="end" class="fill-[var(--neutral-700)] text-[8px] font-mono">{{ formatValue(value) }}</text>
      </g>
      <line :x1="left" :x2="left" :y1="top" :y2="top + plotHeight" class="stroke-[var(--color-divider)]" stroke-width="1" />
      <line :x1="left" :x2="width - right" :y1="top + plotHeight" :y2="top + plotHeight" class="stroke-[var(--color-divider)]" stroke-width="1" />

      <template v-for="item in series" :key="item.label">
        <path v-for="path in paths(item.points)" :key="path" :d="path" fill="none" :class="strokeClass(item.token)" stroke-width="1.5" stroke-linecap="square" stroke-linejoin="miter" vector-effect="non-scaling-stroke" />
        <circle
          v-for="point in item.points.filter(point => point.value !== null && Number.isFinite(point.value))"
          :key="`${item.label}-${point.timestamp}`"
          :cx="xFor(point.timestamp)"
          :cy="yFor(point.value as number)"
          r="2"
          :class="dotClass(item.token)"
        >
          <title>{{ item.label }} · {{ formatTime(point.timestamp) }} · {{ formatValue(point.value as number) }}</title>
        </circle>
      </template>

      <text v-if="timestamps.length" :x="left" :y="height - 5" class="fill-[var(--neutral-700)] text-[8px] font-mono">{{ formatTime(timestamps[0]) }}</text>
      <text v-if="timestamps.length > 1" :x="width - right" :y="height - 5" text-anchor="end" class="fill-[var(--neutral-700)] text-[8px] font-mono">{{ formatTime(timestamps[timestamps.length - 1]) }}</text>
    </svg>
    <p v-if="!presentValues.length" class="text-center text-[11px] text-[var(--neutral-700)]">No retained samples in this range.</p>
  </div>
</template>
