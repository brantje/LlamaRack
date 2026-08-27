<script setup lang="ts">
type Summary = {
  users: { total: number; enabled: number }
  huggingface: { configured: boolean; prefix?: string }
  llamacpp: { available: boolean; path?: string; version?: string; fingerprint?: string }
}

const manager = useManager()
const summary = ref<Summary | null>(null)
const error = ref('')

async function load() {
  if (!manager.user.value) return
  error.value = ''
  try {
    summary.value = await manager.request<Summary>('/api/v1/admin/summary')
  } catch (value: any) {
    error.value = value?.data?.error || value?.message || 'Unable to load administration summary'
  }
}

watch(manager.user, user => {
  if (user) void load()
}, { immediate: true })
</script>

<template>
  <div class="space-y-5">
    <div class="flex items-start justify-between gap-4">
      <UPageHeader class="min-w-0 flex-1" headline="ADMINISTRATION" title="Dashboard" description="Security, provider and runtime configuration for the manager." />
      <UButton color="neutral" variant="soft" @click="load">Refresh</UButton>
    </div>
    <UAlert v-if="error" color="error" variant="subtle" :description="error" />

    <UPageGrid v-if="summary" class="lg:grid-cols-3">
      <UPageCard title="Users" description="Equivalent local management accounts." to="/admin/users" icon="i-lucide-users">
        <p class="text-3xl font-bold">{{ summary.users.enabled }} <span class="text-base font-normal text-muted">enabled / {{ summary.users.total }} total</span></p>
      </UPageCard>
      <UPageCard title="Hugging Face" description="Provider credential status." to="/admin/huggingface" icon="i-lucide-sparkles">
        <UBadge :color="summary.huggingface.configured ? 'success' : 'neutral'" variant="subtle">{{ summary.huggingface.configured ? 'Configured' : 'Not configured' }}</UBadge>
      </UPageCard>
      <UPageCard title="llama.cpp" description="Detected backend binary." to="/admin/llamacpp" icon="i-lucide-terminal-square">
        <UBadge :color="summary.llamacpp.available ? 'success' : 'warning'" variant="subtle">{{ summary.llamacpp.available ? (summary.llamacpp.version || 'Available') : 'Unavailable' }}</UBadge>
      </UPageCard>
    </UPageGrid>
    <div v-else class="grid gap-4 md:grid-cols-3"><USkeleton v-for="n in 3" :key="n" class="h-40 w-full" /></div>
  </div>
</template>
