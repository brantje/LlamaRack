<script setup lang="ts">
const props = withDefaults(defineProps<{
  variant?: 'ready' | 'pending' | 'neutral' | 'failed'
}>(), { variant: 'neutral' })

const classes = computed(() => ({
  ready: 'border border-transparent bg-[var(--success-100)] text-[var(--success-700)]',
  pending: 'border border-[var(--neutral-700)] bg-transparent text-[var(--neutral-800)]',
  neutral: 'border border-transparent bg-[var(--neutral-300)] text-[var(--neutral-900)]',
  failed: 'border border-[var(--danger-700)] bg-[var(--danger-100)] text-[var(--danger-700)]'
}[props.variant]))
</script>

<template>
  <span class="inline-flex items-center px-[10px] py-[3px] text-[length:var(--font-size-table-header)] font-medium leading-none tracking-[.02em]" :class="classes">
    <slot />
  </span>
</template>
