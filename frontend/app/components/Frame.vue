<script setup lang="ts">
defineOptions({ inheritAttrs: false })
</script>

<template>
  <div v-bind="$attrs" class="border border-[var(--color-divider)] bg-[var(--color-surface)] shadow-none">
    <slot />
  </div>
</template>
