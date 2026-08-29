<script setup lang="ts">
import type { Profile } from '~/composables/useManager'

type Row = { id: number; key: string; value: string }
const props = defineProps<{ profile?: Profile | null }>()
const options = defineModel<Record<string, string>>({ required: true })
const rows = ref<Row[]>([])
let nextID = 1
let committing = false

function syncRows() {
  if (committing) return
  rows.value = Object.entries(options.value || {}).sort(([a], [b]) => a.localeCompare(b)).map(([key, value]) => ({ id: nextID++, key, value }))
}
function commitRows() {
  const next: Record<string, string> = {}
  for (const row of rows.value) {
    const key = row.key.trim().replace(/^--+/, '')
    if (key) next[key] = row.value
  }
  committing = true
  options.value = next
  nextTick(() => { committing = false })
}
function addOption() { rows.value.push({ id: nextID++, key: '', value: '' }) }
function removeOption(id: number) { rows.value = rows.value.filter(row => row.id !== id); commitRows() }
function note(key: string) {
  const normalized = key.trim().replace(/^--+/, '')
  const option = props.profile?.options.find(item => item.key === normalized)
  return option?.description || option?.value_hint || 'Custom llama.cpp option'
}

watch(options, syncRows, { deep: true, immediate: true })
</script>

<template>
  <div data-testid="admin-global-defaults-editor">
    <div v-if="rows.length" class="border-y border-[var(--color-divider)]">
      <div class="hidden grid-cols-[220px_minmax(160px,1fr)_minmax(220px,1.2fr)_auto] gap-3 border-b border-[var(--color-divider)] px-1 py-2 text-[10.5px] font-semibold text-[var(--neutral-700)] md:grid">
        <span>Flag</span><span>Value</span><span>Note</span><span></span>
      </div>
      <div v-for="row in rows" :key="row.id" class="grid gap-3 border-b border-[var(--color-divider)] px-1 py-3 last:border-b-0 md:grid-cols-[220px_minmax(160px,1fr)_minmax(220px,1.2fr)_auto] md:items-center" data-testid="admin-global-default-row">
        <UInput v-model="row.key" class="w-full font-mono" placeholder="flag" aria-label="llama.cpp flag" @update:model-value="commitRows" />
        <UInput v-model="row.value" class="w-full font-mono" placeholder="value" aria-label="llama.cpp option value" @update:model-value="commitRows" />
        <p class="text-xs leading-5 text-[var(--neutral-700)]">{{ note(row.key) }}</p>
        <AppButton intent="ghost" size="xs" type="button" @click="removeOption(row.id)">Remove</AppButton>
      </div>
    </div>
    <p v-else class="border-y border-[var(--color-divider)] py-4 text-xs text-[var(--neutral-700)]">No global defaults.</p>
    <AppButton class="mt-3" intent="secondary" size="xs" type="button" data-testid="add-global-option" @click="addOption">Add option</AppButton>
  </div>
</template>
