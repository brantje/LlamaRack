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
  <UCard data-testid="discover-repository-header">
    <div class="space-y-4">
      <div class="flex flex-wrap items-start justify-between gap-4">
        <div class="min-w-0">
          <p class="mb-1 text-xs font-extrabold tracking-[0.18em] text-dimmed">REPOSITORY</p>
          <h2 class="break-words text-2xl font-bold">{{ model.id }}</h2>
          <p v-if="model.author" class="mt-1 text-sm text-muted">Published by {{ model.author }}</p>
        </div>
        <div class="flex flex-wrap gap-2">
          <UBadge v-if="model.private" color="warning">Private</UBadge>
          <UBadge v-if="model.gated" color="warning">Gated</UBadge>
          <UBadge color="neutral" variant="soft">GGUF</UBadge>
        </div>
      </div>

      <dl class="grid gap-x-8 gap-y-3 border-y border-default py-4 text-sm sm:grid-cols-2 lg:grid-cols-4" data-testid="repository-metadata">
        <div v-if="model.parameter_count"><dt class="text-xs font-medium uppercase tracking-wide text-dimmed">Model size</dt><dd class="mt-1 font-semibold">{{ formatParameters(model.parameter_count) }}</dd></div>
        <div v-if="architecture"><dt class="text-xs font-medium uppercase tracking-wide text-dimmed">Architecture</dt><dd class="mt-1 font-semibold">{{ architecture }}</dd></div>
        <div v-if="contextCapability"><dt class="text-xs font-medium uppercase tracking-wide text-dimmed">Context capability</dt><dd class="mt-1 font-semibold">{{ formatContext(contextCapability) }}</dd></div>
        <div v-if="model.card_metadata?.pipeline_tag"><dt class="text-xs font-medium uppercase tracking-wide text-dimmed">Task</dt><dd class="mt-1 font-semibold">{{ model.card_metadata.pipeline_tag }}</dd></div>
        <div v-if="model.card_metadata?.license"><dt class="text-xs font-medium uppercase tracking-wide text-dimmed">License</dt><dd class="mt-1 font-semibold">{{ model.card_metadata.license }}</dd></div>
        <div v-if="model.card_metadata?.library_name"><dt class="text-xs font-medium uppercase tracking-wide text-dimmed">Library</dt><dd class="mt-1 font-semibold">{{ model.card_metadata.library_name }}</dd></div>
        <div v-if="formatUpdated(model.last_modified)"><dt class="text-xs font-medium uppercase tracking-wide text-dimmed">Updated</dt><dd class="mt-1 font-semibold">{{ formatUpdated(model.last_modified) }}</dd></div>
        <div><dt class="text-xs font-medium uppercase tracking-wide text-dimmed">Hugging Face</dt><dd class="mt-1 font-semibold">↓ {{ model.downloads.toLocaleString() }} · ♡ {{ model.likes.toLocaleString() }}</dd></div>
      </dl>

      <div v-if="model.card_metadata?.base_models?.length || model.card_metadata?.languages?.length || usefulTags.length" class="flex flex-wrap items-center gap-2 text-sm" data-testid="repository-tags">
        <span v-if="model.card_metadata?.base_models?.length" class="mr-1 text-muted">Base model</span>
        <UBadge v-for="baseModel in model.card_metadata?.base_models || []" :key="`base-${baseModel}`" color="primary" variant="soft">{{ baseModel }}</UBadge>
        <UBadge v-for="language in model.card_metadata?.languages || []" :key="`language-${language}`" color="neutral" variant="soft">{{ language.toUpperCase() }}</UBadge>
        <UBadge v-for="tag in usefulTags" :key="tag" color="neutral" variant="soft">{{ tag }}</UBadge>
      </div>

      <div v-if="description" class="border-t border-default pt-4" data-testid="repository-description">
        <p class="text-sm leading-6 text-muted">{{ descriptionPreview }}</p>
        <UCollapsible v-if="hasLongDescription" class="mt-2">
          <UButton color="neutral" variant="link" size="xs" trailing-icon="i-lucide-chevron-down" class="px-0">Read full description</UButton>
          <template #content>
            <p class="mt-2 max-w-5xl whitespace-pre-line text-sm leading-6 text-muted" data-testid="repository-description-full">{{ description }}</p>
          </template>
        </UCollapsible>
      </div>
    </div>
  </UCard>
</template>
