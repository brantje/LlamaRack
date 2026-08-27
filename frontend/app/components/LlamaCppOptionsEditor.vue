<script setup lang="ts">
type OptionDefinition = {
  key: string
  value_hint?: string
  description?: string
  kind?: string
  choices?: string[]
  unsupported?: boolean
}
type EffectiveConfig = {
  global: Record<string, string>
  model: Record<string, string>
  instance: Record<string, string>
  values: Record<string, string>
  sources: Record<string, string>
}
type ConfigResponse = {
  profile?: { options?: OptionDefinition[] }
  effective?: EffectiveConfig
  unsupported?: string[]
}

const props = withDefaults(defineProps<{
  modelValue: Record<string, string>
  scope: 'global' | 'model' | 'instance'
  modelId?: string
  instanceId?: string
  defaultOpen?: boolean
}>(), { modelId: '', instanceId: '', defaultOpen: true })
const emit = defineEmits<{ 'update:modelValue': [value: Record<string, string>] }>()

const manager = useManager()
const mode = ref<'basic' | 'advanced'>('basic')
const search = ref('')
const loading = ref(false)
const loadError = ref('')
const config = ref<ConfigResponse | null>(null)

const protectedKeys = new Set(['model', 'host', 'port', 'device', 'split-mode', 'main-gpu'])
const basicKeys = new Set([
  'ctx-size', 'n-gpu-layers', 'gpu-layers', 'threads', 'threads-batch', 'batch-size', 'ubatch-size',
  'parallel', 'flash-attn', 'jinja', 'reasoning-format', 'reasoning-budget', 'embeddings', 'reranking',
  'cache-type-k', 'cache-type-v', 'kv-unified', 'spec-type', 'spec-draft-model', 'mmproj', 'draft-max', 'draft-min', 'tensor-split'
])

const scopeLabel = computed(() => props.scope === 'global' ? 'Global' : props.scope === 'model' ? 'Model' : 'Instance')
const overrides = computed(() => props.modelValue || {})
const overrideCount = computed(() => Object.keys(overrides.value).length)
const editorTitle = computed(() => `${scopeLabel.value} llama.cpp configuration`)
const editorSummary = computed(() => {
  const count = overrideCount.value
  if (props.scope === 'global') return count === 1 ? '1 default configured' : `${count} defaults configured`
  if (count === 0) return 'No overrides configured · inheriting all values'
  return count === 1 ? '1 override configured · remaining values inherited' : `${count} overrides configured · remaining values inherited`
})
const inherited = computed(() => {
  const effective = config.value?.effective
  if (!effective || props.scope === 'global') return {} as Record<string, string>
  if (props.scope === 'model') return { ...(effective.global || {}) }
  return { ...(effective.global || {}), ...(effective.model || {}) }
})
const inheritedSources = computed(() => {
  const effective = config.value?.effective
  const out: Record<string, string> = {}
  if (!effective || props.scope === 'global') return out
  for (const key of Object.keys(effective.global || {})) out[key] = 'global'
  if (props.scope === 'instance') for (const key of Object.keys(effective.model || {})) out[key] = 'model'
  return out
})

const allOptions = computed<OptionDefinition[]>(() => {
  const discovered = [...(config.value?.profile?.options || manager.profile.value?.options || [])]
  const overriddenKeys = new Set(Object.keys(overrides.value))
  const known = new Set(discovered.map(option => option.key))
  for (const key of overriddenKeys) {
    if (!known.has(key)) discovered.push({ key, description: 'Retained from an older llama-server schema.', kind: 'string', unsupported: true })
  }
  return discovered.sort((a, b) => {
    const aOverridden = overriddenKeys.has(a.key)
    const bOverridden = overriddenKeys.has(b.key)
    if (aOverridden !== bOverridden) return aOverridden ? -1 : 1
    return a.key.localeCompare(b.key)
  })
})
const visibleOptions = computed(() => {
  const term = search.value.trim().toLowerCase()
  return allOptions.value.filter((option) => {
    if (mode.value === 'basic' && (!basicKeys.has(option.key) || protectedKeys.has(option.key))) return false
    if (!term) return true
    return option.key.toLowerCase().includes(term) || (option.description || '').toLowerCase().includes(term)
  })
})

function endpoint() {
  const params = new URLSearchParams()
  if (props.modelId) params.set('model_id', props.modelId)
  if (props.instanceId) params.set('instance_id', props.instanceId)
  const query = params.toString()
  return `/api/v1/llamacpp/config${query ? `?${query}` : ''}`
}

async function loadConfig() {
  loading.value = true
  loadError.value = ''
  try {
    const result = await manager.request<ConfigResponse>(endpoint())
    config.value = result && typeof result === 'object' && !Array.isArray(result) ? result : null
  } catch (error: any) {
    config.value = null
    loadError.value = error?.data?.error || error?.message || 'Unable to load llama.cpp configuration'
  } finally {
    loading.value = false
  }
}

watch(() => [props.modelId, props.instanceId], () => void loadConfig())
onMounted(() => void loadConfig())

function isOverridden(key: string) {
  return Object.prototype.hasOwnProperty.call(overrides.value, key)
}
function effectiveValue(key: string) {
  return isOverridden(key) ? overrides.value[key] : inherited.value[key]
}
function effectiveSource(key: string) {
  if (protectedKeys.has(key)) return 'manager'
  if (isOverridden(key)) return props.scope
  return inheritedSources.value[key] || 'upstream'
}
function kind(option: OptionDefinition) {
  if (option.kind) return option.kind
  if (!option.value_hint) return 'boolean'
  return 'string'
}
function choices(option: OptionDefinition) {
  if (option.choices?.length) return option.choices
  const hint = (option.value_hint || '').replace(/^[\[<]|[\]>]$/g, '')
  return hint.includes('|') ? hint.split('|').map(value => value.trim()).filter(Boolean) : []
}
function updateValue(key: string, value: string) {
  emit('update:modelValue', { ...overrides.value, [key]: value })
}
function enableOverride(option: OptionDefinition) {
  if (protectedKeys.has(option.key) || option.unsupported) return
  let value = inherited.value[option.key]
  if (value === undefined) {
    if (kind(option) === 'boolean') value = 'true'
    else if (kind(option) === 'enum') value = choices(option)[0] || ''
    else value = ''
  }
  updateValue(option.key, value)
}
function removeOverride(key: string) {
  const next = { ...overrides.value }
  delete next[key]
  emit('update:modelValue', next)
}
function badgeColor(source: string): 'primary' | 'success' | 'warning' | 'neutral' {
  if (source === 'instance') return 'primary'
  if (source === 'model') return 'success'
  if (source === 'global') return 'warning'
  return 'neutral'
}
</script>

<template>
  <UCollapsible :default-open="defaultOpen" class="space-y-4">
    <template #default="{ open }">
      <UButton type="button" color="neutral" variant="soft" class="w-full">
        <span class="flex w-full items-center justify-between gap-3 text-left">
          <span class="min-w-0">
            <span class="block font-semibold">{{ editorTitle }}</span>
            <span class="block text-xs font-normal text-muted">{{ editorSummary }}</span>
          </span>
          <span class="flex shrink-0 items-center gap-2">
            <UBadge size="sm" color="neutral" variant="subtle">{{ overrideCount }}</UBadge>
            <UIcon :name="open ? 'i-lucide-chevron-up' : 'i-lucide-chevron-down'" class="size-4" />
          </span>
        </span>
      </UButton>
    </template>

    <template #content>
      <div class="space-y-4 pt-1">
        <div class="flex flex-wrap items-center justify-between gap-3">
          <p class="text-xs text-muted">Only overrides are stored at this layer; remove an override to inherit again.</p>
          <div class="flex items-center gap-2">
            <UButton type="button" :variant="mode === 'basic' ? 'solid' : 'soft'" size="sm" @click="mode = 'basic'">Basic</UButton>
            <UButton type="button" :variant="mode === 'advanced' ? 'solid' : 'soft'" size="sm" @click="mode = 'advanced'">Advanced</UButton>
          </div>
        </div>

        <UAlert v-if="loadError" color="warning" variant="subtle" :description="loadError" />
        <UInput v-if="mode === 'advanced'" v-model="search" class="w-full" icon="i-lucide-search" placeholder="Search all detected llama-server options" />
        <div v-if="loading" class="space-y-2"><USkeleton v-for="n in 4" :key="n" class="h-20 w-full" /></div>

        <div v-else class="space-y-2">
          <UCard v-for="option in visibleOptions" :key="option.key" :ui="{ body: 'p-4 sm:p-4' }">
            <div class="flex flex-col gap-3 lg:flex-row lg:items-start lg:justify-between">
              <div class="min-w-0 flex-1">
                <div class="flex flex-wrap items-center gap-2">
                  <code class="font-mono text-sm font-semibold">--{{ option.key }}</code>
                  <UBadge size="sm" variant="subtle" :color="badgeColor(effectiveSource(option.key))">{{ effectiveSource(option.key) }}</UBadge>
                  <UBadge v-if="protectedKeys.has(option.key)" size="sm" color="neutral" variant="outline">Manager controlled</UBadge>
                  <UBadge v-if="option.unsupported" size="sm" color="warning" variant="outline">Unsupported · retained</UBadge>
                </div>
                <p v-if="option.description" class="mt-1 text-xs text-muted">{{ option.description }}</p>
                <p v-if="!isOverridden(option.key) && effectiveValue(option.key) !== undefined" class="mt-1 text-xs text-dimmed">Effective inherited value: <code>{{ effectiveValue(option.key) }}</code></p>
                <p v-else-if="!isOverridden(option.key)" class="mt-1 text-xs text-dimmed">Using llama.cpp upstream default.</p>
              </div>

              <div class="w-full space-y-2 lg:w-80">
                <template v-if="isOverridden(option.key)">
                  <UCheckbox
                    v-if="kind(option) === 'boolean' && !option.unsupported"
                    :model-value="overrides[option.key] === 'true'"
                    :label="overrides[option.key] === 'true' ? 'Enabled' : 'Disabled'"
                    @update:model-value="updateValue(option.key, $event ? 'true' : 'false')"
                  />
                  <USelectMenu
                    v-else-if="kind(option) === 'enum' && !option.unsupported"
                    :model-value="overrides[option.key]"
                    class="w-full"
                    :items="choices(option)"
                    @update:model-value="updateValue(option.key, String($event || ''))"
                  />
                  <UInput
                    v-else
                    :model-value="overrides[option.key]"
                    class="w-full font-mono"
                    :disabled="option.unsupported"
                    :placeholder="option.value_hint || 'value'"
                    @update:model-value="updateValue(option.key, String($event || ''))"
                  />
                  <UButton type="button" size="xs" color="neutral" variant="ghost" @click="removeOverride(option.key)">Remove override</UButton>
                </template>
                <UButton v-else-if="!protectedKeys.has(option.key)" type="button" size="xs" color="neutral" variant="soft" @click="enableOverride(option)">Override here</UButton>
              </div>
            </div>
          </UCard>
          <UAlert v-if="!visibleOptions.length" color="neutral" variant="subtle" description="No options match this view. Switch to Advanced to see every detected option." />
        </div>
      </div>
    </template>
  </UCollapsible>
</template>
