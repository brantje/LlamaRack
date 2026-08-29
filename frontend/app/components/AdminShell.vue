<script setup lang="ts">
import AdminSidebar from '~/components/navigation/AdminSidebar.vue'

defineProps<{
  title: string
  description: string
}>()
</script>

<template>
  <div class="flex min-w-0 flex-col gap-6 lg:flex-row" data-testid="admin-shell">
    <AdminSidebar />
    <main class="min-w-0 flex-1 lg:pl-1">
      <div class="mb-5 flex items-start justify-between gap-4">
        <UPageHeader class="min-w-0 flex-1" headline="ADMINISTRATION" :title="title" :description="description" />
        <div v-if="$slots.actions" class="flex shrink-0 flex-wrap justify-end gap-2">
          <slot name="actions" />
        </div>
      </div>
      <slot />
    </main>
  </div>
</template>
