<script setup lang="ts">
const { initializeTheme } = useAppTheme()
onMounted(initializeTheme)
</script>

<template>
  <UApp>
    <div class="min-h-screen bg-[var(--color-bg)] text-[var(--color-text)]">
      <NuxtLayout>
        <NuxtPage />
      </NuxtLayout>
    </div>
  </UApp>
</template>
