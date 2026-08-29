<script setup lang="ts">
defineOptions({ inheritAttrs: false })

const props = withDefaults(defineProps<{
  intent?: 'primary' | 'secondary' | 'ghost' | 'destructive'
}>(), { intent: 'secondary' })

const treatment = computed(() => {
  if (props.intent === 'primary') return { color: 'primary' as const, variant: 'solid' as const, class: '' }
  if (props.intent === 'ghost') return { color: 'neutral' as const, variant: 'ghost' as const, class: '' }
  if (props.intent === 'destructive') return {
    color: 'neutral' as const,
    variant: 'outline' as const,
    class: 'border-[var(--accent-800)] text-[var(--accent-900)] hover:bg-[var(--accent-100)]'
  }
  return { color: 'neutral' as const, variant: 'outline' as const, class: '' }
})
</script>

<template>
  <UButton v-bind="$attrs" :color="treatment.color" :variant="treatment.variant" :class="treatment.class">
    <slot />
  </UButton>
</template>
