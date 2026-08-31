<script setup lang="ts">
const props = withDefaults(defineProps<{
  variant?: 'ready' | 'pending' | 'neutral' | 'failed'
}>(), { variant: 'neutral' })

const classes = computed(() => ({
  ready: 'border border-transparent bg-[var(--accent-100)] text-[var(--accent-800)]',
  pending: 'border border-[var(--color-accent)] bg-transparent text-[var(--accent-700)]',
  neutral: 'border border-transparent bg-[var(--neutral-300)] text-[var(--neutral-900)]',
  failed: 'border border-[var(--accent-800)] bg-transparent text-[var(--accent-800)]'
}[props.variant]))
</script>

<template>
  <span class="inline-flex items-center px-[10px] py-[3px] text-[length:var(--font-size-table-header)] font-medium leading-none tracking-[.02em]" :class="classes">
    <slot />
  </span>
</template>
