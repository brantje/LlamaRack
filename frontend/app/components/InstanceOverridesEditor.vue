<script setup lang="ts">
type OverrideRow = { id: number; key: string; value: string }

const props = withDefaults(defineProps<{ excludeKeys?: string[] }>(), { excludeKeys: () => [] })
const options = defineModel<Record<string, string>>({ required: true })
const manager = useManager()
const rows = ref<OverrideRow[]>([])
let nextID = 1
let committing = false

const excluded = computed(() => new Set(props.excludeKeys))

function syncRows() {
  if (committing) return
  rows.value = Object.entries(options.value || {})
    .filter(([key]) => !excluded.value.has(key))
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([key, value]) => ({ id: nextID++, key, value }))
}

function noteFor(key: string) {
  const normalized = key.trim().replace(/^--+/, '')
  return manager.profile.value?.options.find(option => option.key === normalized)?.description || 'Instance override'
}

function commitRows() {
  const next: Record<string, string> = {}
  for (const [key, value] of Object.entries(options.value || {})) {
    if (excluded.value.has(key)) next[key] = value
  }
  for (const row of rows.value) {
    const key = row.key.trim().replace(/^--+/, '')
    if (!key) continue
    next[key] = row.value
  }
  committing = true
  options.value = next
  nextTick(() => { committing = false })
}

function addOption() {
  rows.value.push({ id: nextID++, key: '', value: '' })
}

function removeOption(id: number) {
  rows.value = rows.value.filter(row => row.id !== id)
  commitRows()
}

watch(options, syncRows, { deep: true, immediate: true })
watch(() => props.excludeKeys.join('\u0000'), syncRows)
</script>

<template>
  <div class="space-y-3" data-testid="instance-overrides-editor">
    <div v-if="rows.length" class="divide-y divide-[var(--color-divider)] border-y border-[var(--color-divider)]">
      <div
        v-for="row in rows"
        :key="row.id"
        class="grid gap-3 py-3 md:grid-cols-[200px_150px_minmax(0,1fr)_auto] md:items-center"
        data-testid="instance-override-row"
      >
        <UInput
          v-model="row.key"
          class="w-full font-mono"
          placeholder="flag"
          aria-label="llama.cpp flag"
          @update:model-value="commitRows"
        />
        <UInput
          v-model="row.value"
          class="w-full font-mono"
          placeholder="value"
          aria-label="llama.cpp option value"
          @update:model-value="commitRows"
        />
        <p class="text-xs text-[var(--neutral-700)]">{{ noteFor(row.key) }}</p>
        <AppButton intent="ghost" size="xs" type="button" @click="removeOption(row.id)">Remove</AppButton>
      </div>
    </div>
    <p v-else class="text-xs text-[var(--neutral-700)]">No Instance-specific overrides.</p>
    <AppButton intent="secondary" size="xs" type="button" data-testid="add-instance-option" @click="addOption">Add option</AppButton>
  </div>
</template>
