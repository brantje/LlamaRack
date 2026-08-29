<script setup lang="ts">
const props = defineProps<{
  label: string
  source: string
}>()

const sourceClass = computed(() => props.source === 'environment' ? 'text-[var(--accent-700)]' : 'text-[var(--neutral-700)]')
</script>

<template>
  <UFormField>
    <template #label>
      <span class="flex w-full items-center justify-between gap-3 text-xs font-semibold">
        <span>{{ label }}</span>
        <span class="font-mono text-[10.5px] font-normal" :class="sourceClass" data-testid="setting-source">{{ source }}</span>
      </span>
    </template>
    <slot />
    <template v-if="$slots.help" #help><slot name="help" /></template>
  </UFormField>
</template>
