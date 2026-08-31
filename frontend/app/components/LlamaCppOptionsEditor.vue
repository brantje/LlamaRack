<script setup lang="ts">
type OptionDefinition = {
  key: string
  value_hint?: string
  description?: string
  kind?: string
  choices?: string[]
  manager_owned?: boolean
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
type StatusVariant = 'ready' | 'pending' | 'neutral' | 'failed'

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

const legacyProtectedKeys = new Set([
  'model', 'host', 'port', 'device', 'split-mode', 'main-gpu',
  'cors-origins', 'cors-methods', 'cors-headers', 'cors-credentials', 'no-cors-credentials',
  'api-key', 'api-key-file'
])
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
  if (count === 0) return 'No overrides configured · inheriting all values · inherited options available below'
  return count === 1 ? '1 override configured · remaining values inherited' : `${count} overrides configured · remaining values inherited`
})
const inherited = computed(() => {
  const effective = config.value?.effective
  if (!effective) return {} as Record<string, string>
  if (Object.keys(effective.values || {}).length) return { ...effective.values }
  return { ...(effective.global || {}), ...(effective.model || {}), ...(effective.instance || {}) }
})
const inheritedSources = computed(() => {
  const effective = config.value?.effective
  if (!effective) return {} as Record<string, string>
  if (Object.keys(effective.sources || {}).length) return { ...effective.sources }
  const out: Record<string, string> = {}
  for (const key of Object.keys(effective.global || {})) out[key] = 'global'
  for (const key of Object.keys(effective.model || {})) out[key] = 'model'
  for (const key of Object.keys(effective.instance || {})) out[key] = 'instance'
  return out
})

const allOptions = computed<OptionDefinition[]>(() => {
  const discovered: OptionDefinition[] = [...(config.value?.profile?.options || manager.profile.value?.options || [])]
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
    if (mode.value === 'basic' && (!basicKeys.has(option.key) || isProtected(option))) return false
    if (!term) return true
    return option.key.toLowerCase().includes(term) || (option.description || '').toLowerCase().includes(term)
  })
})
const visibleOverrides = computed(() => visibleOptions.value.filter(option => isOverridden(option.key)))
const visibleInheritedOptions = computed(() => visibleOptions.value.filter(option => !isOverridden(option.key)))

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

function isProtected(option: OptionDefinition) {
  return option.manager_owned === true || legacyProtectedKeys.has(option.key)
}
function isOverridden(key: string) {
  return Object.prototype.hasOwnProperty.call(overrides.value, key)
}
function effectiveValue(key: string) {
  return isOverridden(key) ? overrides.value[key] : inherited.value[key]
}
function effectiveSource(option: OptionDefinition) {
  if (isProtected(option)) return 'manager'
  if (isOverridden(option.key)) return props.scope
  return inheritedSources.value[option.key] || 'upstream'
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
  if (isProtected(option) || option.unsupported) return
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
function sourceVariant(source: string): StatusVariant {
  if (source === 'instance') return 'ready'
  if (source === 'model') return 'pending'
  if (source === 'global') return 'pending'
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
            <StatusTag variant="neutral">{{ overrideCount }}</StatusTag>
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
            <UButton type="button" :variant="mode === 'basic' ? 'solid' : 'soft'" size="sm" :aria-pressed="mode === 'basic'" @click="mode = 'basic'">Basic</UButton>
            <UButton type="button" :variant="mode === 'advanced' ? 'solid' : 'soft'" size="sm" :aria-pressed="mode === 'advanced'" @click="mode = 'advanced'">Advanced</UButton>
          </div>
        </div>

        <Frame v-if="loadError" class="p-3">
          <div class="flex items-start gap-2"><StatusTag variant="failed">Error</StatusTag><p class="text-xs leading-5 text-[var(--neutral-800)]">{{ loadError }}</p></div>
        </Frame>
        <UInput v-if="mode === 'advanced'" v-model="search" class="w-full" icon="i-lucide-search" placeholder="Search all detected llama-server options" />
        <div v-if="loading" class="space-y-2"><USkeleton v-for="n in 4" :key="n" class="h-20 w-full" /></div>

        <div v-else class="space-y-4">
          <section v-if="visibleOverrides.length" data-testid="llamacpp-configured-overrides">
            <div class="mb-2 flex items-center justify-between gap-3">
              <h3 class="text-xs font-semibold uppercase tracking-[.08em] text-[var(--neutral-800)]">Configured overrides</h3>
              <span class="font-mono text-xs tabular-nums text-[var(--neutral-700)]">{{ visibleOverrides.length }}</span>
            </div>
            <div class="border border-[var(--color-divider)]">
              <div v-for="option in visibleOverrides" :key="option.key" class="border-t border-[var(--color-divider)] p-3 first:border-t-0" data-testid="llamacpp-option-row" data-option-state="override">
                <div class="flex flex-col gap-3 lg:flex-row lg:items-start lg:justify-between">
                  <div class="min-w-0 flex-1">
                    <div class="flex flex-wrap items-center gap-2">
                      <code class="font-mono text-sm font-semibold">--{{ option.key }}</code>
                      <StatusTag :variant="sourceVariant(effectiveSource(option))">{{ effectiveSource(option) }}</StatusTag>
                      <StatusTag v-if="isProtected(option)" variant="neutral">Manager controlled</StatusTag>
                      <StatusTag v-if="option.unsupported" variant="failed">Unsupported · retained</StatusTag>
                    </div>
                    <p v-if="option.description" class="mt-1 text-xs text-muted">{{ option.description }}</p>
                  </div>

                  <div class="w-full space-y-2 lg:w-72">
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
                    <AppButton type="button" size="xs" intent="ghost" @click="removeOverride(option.key)">Remove override</AppButton>
                  </div>
                </div>
              </div>
            </div>
          </section>

          <section v-if="visibleInheritedOptions.length" data-testid="llamacpp-inherited-options">
            <div class="mb-2 flex items-center justify-between gap-3">
              <div>
                <h3 class="text-xs font-semibold uppercase tracking-[.08em] text-[var(--neutral-800)]">Available inherited options</h3>
                <p class="mt-1 text-xs text-dimmed">These values are not stored here until you choose Override.</p>
              </div>
              <span class="font-mono text-xs tabular-nums text-[var(--neutral-700)]">{{ visibleInheritedOptions.length }}</span>
            </div>
            <div class="border border-[var(--color-divider)]">
              <div v-for="option in visibleInheritedOptions" :key="option.key" class="flex flex-col gap-2 border-t border-[var(--color-divider)] p-3 first:border-t-0 sm:flex-row sm:items-center sm:justify-between" data-testid="llamacpp-option-row" data-option-state="inherited">
                <div class="min-w-0 flex-1">
                  <div class="flex flex-wrap items-center gap-2">
                    <code class="font-mono text-sm font-semibold">--{{ option.key }}</code>
                    <StatusTag :variant="sourceVariant(effectiveSource(option))">{{ effectiveSource(option) }}</StatusTag>
                    <StatusTag v-if="isProtected(option)" variant="neutral">Manager controlled</StatusTag>
                  </div>
                  <p v-if="option.description" class="mt-1 text-xs text-muted">{{ option.description }}</p>
                  <p v-if="effectiveValue(option.key) !== undefined" class="mt-1 text-xs text-dimmed">Effective inherited value: <code>{{ effectiveValue(option.key) }}</code></p>
                  <p v-else class="mt-1 text-xs text-dimmed">Using llama.cpp upstream default.</p>
                </div>
                <AppButton v-if="!isProtected(option) && !option.unsupported" type="button" size="xs" intent="secondary" class="shrink-0 self-start sm:self-center" @click="enableOverride(option)">Override here</AppButton>
              </div>
            </div>
          </section>

          <Frame v-if="!visibleOptions.length" class="p-3">
            <div class="flex items-start gap-2"><StatusTag variant="neutral">No options</StatusTag><p class="text-xs leading-5 text-[var(--neutral-800)]">No options match this view. Switch to Advanced to see every detected option.</p></div>
          </Frame>
        </div>
      </div>
    </template>
  </UCollapsible>
</template>
