<script setup lang="ts">
import type { PlaygroundTurnStats } from '~/utils/playgroundTurnStats'

const props = defineProps<{ stats: PlaygroundTurnStats }>()

function formatMS(value?: number, approximate = false) {
  if (!Number.isFinite(value)) return ''
  const number = Number(value)
  const formatted = number < 1000 ? `${Math.round(number)} ms` : `${(number / 1000).toFixed(2)} s`
  return approximate ? `~${formatted}` : formatted
}

function formatRate(value?: number) {
  return Number.isFinite(value) ? `${Number(value).toFixed(2)} tok/s` : ''
}

function formatPercent(value?: number) {
  return Number.isFinite(value) ? `${Number(value).toFixed(1)}%` : ''
}

const summaryParts = computed(() => {
  const parts: string[] = []
  const rate = formatRate(props.stats.generationRate)
  if (rate) parts.push(rate)
  if (Number.isFinite(props.stats.generatedTokens)) parts.push(`${props.stats.generatedTokens} generated`)
  const ttft = formatMS(props.stats.ttftMs, props.stats.ttftEstimated)
  if (ttft) parts.push(`${ttft} TTFT`)
  const wall = formatMS(props.stats.wallMs, props.stats.wallEstimated)
  if (wall) parts.push(`${wall} wall`)
  return parts
})

const promptDetail = computed(() => [
  Number.isFinite(props.stats.promptTokens) ? `${props.stats.promptTokens} tokens` : '',
  formatRate(props.stats.promptRate),
  formatMS(props.stats.promptMs),
  Number.isFinite(props.stats.promptPerTokenMs) ? `${Number(props.stats.promptPerTokenMs).toFixed(2)} ms/token` : ''
].filter(Boolean).join(' · '))

const generationDetail = computed(() => [
  Number.isFinite(props.stats.generatedTokens) ? `${props.stats.generatedTokens} tokens` : '',
  formatRate(props.stats.generationRate),
  formatMS(props.stats.generationMs),
  Number.isFinite(props.stats.generationPerTokenMs) ? `${Number(props.stats.generationPerTokenMs).toFixed(2)} ms/token` : ''
].filter(Boolean).join(' · '))

const cacheDetail = computed(() => [
  Number.isFinite(props.stats.cacheTokens) ? `${props.stats.cacheTokens} reused` : '',
  formatPercent(props.stats.cachePercent)
].filter(Boolean).join(' · '))

const draftDetail = computed(() => {
  if (!Number.isFinite(props.stats.draftProposed) && !Number.isFinite(props.stats.draftAccepted)) return ''
  const accepted = Number.isFinite(props.stats.draftAccepted) ? String(props.stats.draftAccepted) : '—'
  const proposed = Number.isFinite(props.stats.draftProposed) ? String(props.stats.draftProposed) : '—'
  return [`${accepted} / ${proposed} accepted`, formatPercent(props.stats.draftAcceptancePercent)].filter(Boolean).join(' · ')
})

const contextDetail = computed(() => {
  if (!Number.isFinite(props.stats.contextUsed)) return ''
  const used = String(props.stats.contextUsed)
  const max = Number.isFinite(props.stats.contextMax) ? String(props.stats.contextMax) : ''
  return [max ? `${used} / ${max}` : used, formatPercent(props.stats.contextPercent)].filter(Boolean).join(' · ')
})
</script>

<template>
  <UCollapsible v-if="summaryParts.length" class="mt-2" data-testid="playground-turn-stats">
    <UButton
      color="neutral"
      variant="link"
      size="xs"
      trailing-icon="i-lucide-chevron-down"
      class="h-auto p-0 font-mono text-[length:var(--font-size-table-header)] font-normal tabular-nums text-[var(--neutral-700)]"
      data-testid="playground-turn-stats-summary"
    >
      {{ summaryParts.join(' · ') }}
    </UButton>
    <template #content>
      <dl
        class="mt-2 divide-y divide-[var(--color-divider)] border-y border-[var(--color-divider)] font-mono text-[length:var(--font-size-table-header)] tabular-nums"
        data-testid="playground-turn-stats-details"
      >
        <div v-if="promptDetail" class="grid grid-cols-[7rem_1fr] gap-2 py-1.5"><dt class="text-[var(--neutral-700)]">Prompt</dt><dd>{{ promptDetail }}</dd></div>
        <div v-if="generationDetail" class="grid grid-cols-[7rem_1fr] gap-2 py-1.5"><dt class="text-[var(--neutral-700)]">Generation</dt><dd>{{ generationDetail }}</dd></div>
        <div v-if="Number.isFinite(stats.queueMs)" class="grid grid-cols-[7rem_1fr] gap-2 py-1.5"><dt class="text-[var(--neutral-700)]">Queue</dt><dd>{{ formatMS(stats.queueMs) }}</dd></div>
        <div v-if="Number.isFinite(stats.ttftMs)" class="grid grid-cols-[7rem_1fr] gap-2 py-1.5"><dt class="text-[var(--neutral-700)]">First token</dt><dd>{{ formatMS(stats.ttftMs, stats.ttftEstimated) }}</dd></div>
        <div v-if="Number.isFinite(stats.wallMs)" class="grid grid-cols-[7rem_1fr] gap-2 py-1.5"><dt class="text-[var(--neutral-700)]">Wall</dt><dd>{{ formatMS(stats.wallMs, stats.wallEstimated) }}</dd></div>
        <div v-if="cacheDetail" class="grid grid-cols-[7rem_1fr] gap-2 py-1.5"><dt class="text-[var(--neutral-700)]">Cache</dt><dd>{{ cacheDetail }}</dd></div>
        <div v-if="draftDetail" class="grid grid-cols-[7rem_1fr] gap-2 py-1.5"><dt class="text-[var(--neutral-700)]">Draft</dt><dd>{{ draftDetail }}</dd></div>
        <div v-if="contextDetail" class="grid grid-cols-[7rem_1fr] gap-2 py-1.5"><dt class="text-[var(--neutral-700)]">Context</dt><dd>{{ contextDetail }}</dd></div>
        <div v-if="stats.finishReason" class="grid grid-cols-[7rem_1fr] gap-2 py-1.5"><dt class="text-[var(--neutral-700)]">Stop</dt><dd>{{ stats.finishReason }}</dd></div>
        <div v-if="Number.isFinite(stats.toolCallCount)" class="grid grid-cols-[7rem_1fr] gap-2 py-1.5"><dt class="text-[var(--neutral-700)]">Tool calls</dt><dd>{{ stats.toolCallCount }}</dd></div>
      </dl>
    </template>
  </UCollapsible>
</template>
