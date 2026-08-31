<script setup lang="ts">
defineOptions({ inheritAttrs: false })

const props = withDefaults(defineProps<{
  collapsible?: boolean
  defaultOpen?: boolean
  title?: string
  description?: string
}>(), {
  collapsible: false,
  defaultOpen: false,
  title: '',
  description: ''
})

const open = defineModel<boolean>('open')
const expanded = ref(props.defaultOpen)

watch(() => props.defaultOpen, (value) => {
  if (open.value === undefined) expanded.value = value
})

watch(open, (value) => {
  if (value !== undefined) expanded.value = value
})

const isOpen = computed(() => (open.value !== undefined ? open.value : expanded.value))

function toggleOpen() {
  const next = !isOpen.value
  if (open.value !== undefined) {
    open.value = next
    return
  }
  expanded.value = next
}
</script>

<template>
  <div v-bind="$attrs" class="relative border border-[var(--color-divider)] bg-[var(--color-surface)] shadow-none">
    <div class="flex w-full" :class="{ 'cursor-pointer': collapsible && !isOpen }" @click="toggleOpen">
      <div class="mb-4" v-if="(title || description) && collapsible && !isOpen">
          <h2 class="mt-1 text-base font-semibold">{{ title }}</h2>
          <p class="mt-1 text-xs text-[var(--neutral-700)]">{{ description }}</p>
      </div>
      <UButton
      v-if="collapsible"
      type="button"
      color="neutral"
      variant="ghost"
      size="xs"
      class="absolute top-2 right-2 z-10"
      :icon="isOpen ? 'i-lucide-chevron-up' : 'i-lucide-chevron-down'"
      :aria-label="isOpen ? 'Collapse section' : 'Expand section'"
      :aria-expanded="isOpen"
      data-testid="frame-collapse-toggle"
    />
    </div>
    <div v-if="!collapsible || isOpen" class="contents">
      <slot />
    </div>
  </div>
</template>
