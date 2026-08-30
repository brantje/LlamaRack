<script setup lang="ts">
import AdminSidebar from '~/components/navigation/AdminSidebar.vue'

withDefaults(defineProps<{
  title: string
  description: string
  headline?: string
}>(), {
  headline: 'ADMINISTRATION'
})
</script>

<template>
  <div class="flex min-w-0 flex-col gap-6 lg:flex-row" data-testid="admin-shell">
    <AdminSidebar />
    <main class="min-w-0 flex-1 lg:pl-1">
      <div class="mb-5 flex flex-col items-stretch gap-4 sm:flex-row sm:items-start sm:justify-between">
        <UPageHeader class="min-w-0 flex-1" :headline="headline" :title="title" :description="description" />
        <div v-if="$slots.actions" class="flex w-full flex-wrap justify-start gap-2 sm:w-auto sm:shrink-0 sm:justify-end">
          <slot name="actions" />
        </div>
      </div>
      <slot />
    </main>
  </div>
</template>
