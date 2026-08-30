<script setup lang="ts">
type CardMetadata = {
  license?: string
  pipeline_tag?: string
  library_name?: string
  base_models?: string[]
  languages?: string[]
}

type Model = {
  id: string
  author?: string
  description?: string
  downloads: number
  likes: number
  last_modified?: string
  parameter_count?: number
  tags?: string[]
  private: boolean
  gated: boolean
  card_metadata?: CardMetadata
}

type Recommendations = {
  context_capability?: number
  metadata?: { architecture?: string; context_length?: number }
}

const props = defineProps<{ model: Model; recommendations?: Recommendations | null }>()

const descriptionPreviewLength = 280
const description = computed(() => String(props.model.description || '').trim())
const hasLongDescription = computed(() => description.value.length > descriptionPreviewLength)
const descriptionPreview = computed(() => {
  if (!hasLongDescription.value) return description.value
  const preview = description.value.slice(0, descriptionPreviewLength).trimEnd()
  const lastSpace = preview.lastIndexOf(' ')
  return `${lastSpace > 200 ? preview.slice(0, lastSpace) : preview}…`
})
const architecture = computed(() => props.recommendations?.metadata?.architecture || '')
const contextCapability = computed(() => props.recommendations?.context_capability || props.recommendations?.metadata?.context_length || 0)
const usefulTags = computed(() => {
  const hidden = new Set(['gguf', 'llama.cpp', 'quantized'])
  return (props.model.tags || [])
    .filter(tag => tag && !hidden.has(tag.toLowerCase()) && !tag.toLowerCase().startsWith('license:') && !tag.toLowerCase().startsWith('base_model:'))
    .slice(0, 8)
})

function formatParameters(value?: number) {
  if (!value || value <= 0) return ''
  for (const unit of [
    { threshold: 1e12, suffix: 'T' },
    { threshold: 1e9, suffix: 'B' },
    { threshold: 1e6, suffix: 'M' },
    { threshold: 1e3, suffix: 'K' }
  ]) {
    if (value < unit.threshold) continue
    const amount = value / unit.threshold
    const digits = amount >= 100 || Number.isInteger(amount) ? 0 : 1
    return `${amount.toFixed(digits).replace(/\.0$/, '')}${unit.suffix} params`
  }
  return `${value.toLocaleString()} params`
}

function formatContext(value: number) {
  if (value >= 1024 * 1024 && value % (1024 * 1024) === 0) return `${value / (1024 * 1024)}M`
  if (value >= 1024) return `${Math.round(value / 1024)}K`
  return String(value)
}

function formatUpdated(value?: string) {
  if (!value) return ''
  const timestamp = new Date(value).getTime()
  if (!Number.isFinite(timestamp)) return ''
  const seconds = Math.max(0, Math.floor((Date.now() - timestamp) / 1000))
  if (seconds < 60) return 'Just now'
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m ago`
  if (seconds < 86400) return `${Math.floor(seconds / 3600)}h ago`
  if (seconds < 86400 * 30) return `${Math.floor(seconds / 86400)}d ago`
  return new Date(timestamp).toLocaleDateString()
}
</script>

<template>
  <Frame class="p-5" data-testid="discover-repository-header">
    <div class="space-y-4">
      <div class="flex flex-wrap items-start justify-between gap-4">
        <div class="min-w-0">
          <p class="mb-1 text-[10px] font-semibold uppercase tracking-[.1em] text-[var(--neutral-700)]">REPOSITORY SUMMARY</p>
          <h2 class="break-words font-mono text-xl font-semibold text-[var(--color-text)]">{{ model.id }}</h2>
          <p v-if="model.author" class="mt-1 text-sm text-[var(--neutral-800)]">Published by {{ model.author }}</p>
        </div>
        <div class="flex flex-wrap gap-2">
          <StatusTag v-if="model.private" variant="pending">Private</StatusTag>
          <StatusTag v-if="model.gated" variant="pending">Gated</StatusTag>
          <StatusTag variant="neutral">GGUF</StatusTag>
        </div>
      </div>

      <dl class="grid gap-x-8 gap-y-3 border-y border-[var(--color-divider)] py-4 text-sm sm:grid-cols-2 lg:grid-cols-4" data-testid="repository-metadata">
        <div v-if="model.parameter_count"><dt class="text-[10px] font-medium uppercase tracking-[.08em] text-[var(--neutral-700)]">Model size</dt><dd class="mt-1 font-mono font-semibold tabular-nums">{{ formatParameters(model.parameter_count) }}</dd></div>
        <div v-if="architecture"><dt class="text-[10px] font-medium uppercase tracking-[.08em] text-[var(--neutral-700)]">Architecture</dt><dd class="mt-1 font-mono font-semibold">{{ architecture }}</dd></div>
        <div v-if="contextCapability"><dt class="text-[10px] font-medium uppercase tracking-[.08em] text-[var(--neutral-700)]">Context capability</dt><dd class="mt-1 font-mono font-semibold tabular-nums">{{ formatContext(contextCapability) }}</dd></div>
        <div v-if="model.card_metadata?.pipeline_tag"><dt class="text-[10px] font-medium uppercase tracking-[.08em] text-[var(--neutral-700)]">Task</dt><dd class="mt-1 font-semibold">{{ model.card_metadata.pipeline_tag }}</dd></div>
        <div v-if="model.card_metadata?.license"><dt class="text-[10px] font-medium uppercase tracking-[.08em] text-[var(--neutral-700)]">License</dt><dd class="mt-1 font-semibold">{{ model.card_metadata.license }}</dd></div>
        <div v-if="model.card_metadata?.library_name"><dt class="text-[10px] font-medium uppercase tracking-[.08em] text-[var(--neutral-700)]">Library</dt><dd class="mt-1 font-semibold">{{ model.card_metadata.library_name }}</dd></div>
        <div v-if="formatUpdated(model.last_modified)"><dt class="text-[10px] font-medium uppercase tracking-[.08em] text-[var(--neutral-700)]">Updated</dt><dd class="mt-1 font-mono font-semibold tabular-nums">{{ formatUpdated(model.last_modified) }}</dd></div>
        <div><dt class="text-[10px] font-medium uppercase tracking-[.08em] text-[var(--neutral-700)]">Hugging Face</dt><dd class="mt-1 font-mono font-semibold tabular-nums">↓ {{ model.downloads.toLocaleString() }} · ♡ {{ model.likes.toLocaleString() }}</dd></div>
      </dl>

      <div v-if="model.card_metadata?.base_models?.length || model.card_metadata?.languages?.length || usefulTags.length" class="flex flex-wrap items-center gap-2 text-sm" data-testid="repository-tags">
        <span v-if="model.card_metadata?.base_models?.length" class="mr-1 text-[var(--neutral-800)]">Base model</span>
        <StatusTag v-for="baseModel in model.card_metadata?.base_models || []" :key="`base-${baseModel}`" variant="pending">{{ baseModel }}</StatusTag>
        <StatusTag v-for="language in model.card_metadata?.languages || []" :key="`language-${language}`" variant="neutral">{{ language.toUpperCase() }}</StatusTag>
        <StatusTag v-for="tag in usefulTags" :key="tag" variant="neutral">{{ tag }}</StatusTag>
      </div>

      <div v-if="description" class="border-t border-[var(--color-divider)] pt-4" data-testid="repository-description">
        <p class="text-sm leading-6 text-[var(--neutral-800)]">{{ descriptionPreview }}</p>
        <UCollapsible v-if="hasLongDescription" class="mt-2">
          <AppButton intent="ghost" size="xs" trailing-icon="i-lucide-chevron-down" class="px-0">Read full description</AppButton>
          <template #content>
            <p class="mt-2 max-w-5xl whitespace-pre-line text-sm leading-6 text-[var(--neutral-800)]" data-testid="repository-description-full">{{ description }}</p>
          </template>
        </UCollapsible>
      </div>
    </div>
  </Frame>
</template>
