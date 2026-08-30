<script setup lang="ts">
defineOptions({ inheritAttrs: false })

type ButtonIntent = 'primary' | 'secondary' | 'ghost'
type ButtonTone = 'default' | 'destructive'

const props = withDefaults(defineProps<{
  intent?: ButtonIntent
  tone?: ButtonTone
}>(), { intent: 'secondary', tone: 'default' })

const treatment = computed(() => {
  if (props.tone === 'destructive') {
    if (props.intent === 'primary') return {
      color: 'neutral' as const,
      variant: 'solid' as const,
      class: 'bg-[var(--color-danger)] text-[var(--color-on-danger)] hover:bg-[var(--danger-700)]'
    }
    if (props.intent === 'ghost') return {
      color: 'neutral' as const,
      variant: 'ghost' as const,
      class: 'text-[var(--danger-700)] hover:bg-[var(--danger-100)]'
    }
    return {
      color: 'neutral' as const,
      variant: 'outline' as const,
      class: 'border-[var(--color-danger)] text-[var(--danger-700)] hover:bg-[var(--danger-100)]'
    }
  }
  if (props.intent === 'primary') return { color: 'primary' as const, variant: 'solid' as const, class: '' }
  if (props.intent === 'ghost') return { color: 'neutral' as const, variant: 'ghost' as const, class: '' }
  return { color: 'neutral' as const, variant: 'outline' as const, class: '' }
})
</script>

<template>
  <UButton v-bind="$attrs" :color="treatment.color" :variant="treatment.variant" :class="treatment.class">
    <slot />
  </UButton>
</template>
