<script setup lang="ts">
const props = defineProps<{
  label: string
  source: string
}>()

const sourceClass = computed(() => props.source === 'environment' ? 'text-[var(--accent-800)]' : 'text-[var(--neutral-800)]')
</script>

<template>
  <UFormField>
    <template #label>
      <span class="flex w-full flex-col items-start gap-1 text-xs font-semibold sm:flex-row sm:items-center sm:justify-between sm:gap-3">
        <span>{{ label }}</span>
        <span class="font-mono text-xs font-normal" :class="sourceClass" data-testid="setting-source">source: {{ source }}</span>
      </span>
    </template>
    <slot />
    <template v-if="$slots.help" #help><slot name="help" /></template>
  </UFormField>
</template>
