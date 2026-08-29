<script setup lang="ts">
type Summary = {
  users: { total: number; enabled: number }
  huggingface: { configured: boolean; prefix?: string }
  llamacpp: { available: boolean; path?: string; version?: string; fingerprint?: string }
}

const manager = useManager()
const summary = ref<Summary | null>(null)
const error = ref('')

function isSummary(value: unknown): value is Summary {
  if (!value || typeof value !== 'object') return false
  const candidate = value as Partial<Summary>
  return Boolean(candidate.users && candidate.huggingface && candidate.llamacpp)
}

async function load() {
  if (!manager.user.value) return
  error.value = ''
  try {
    const value = await manager.request<Summary>('/api/v1/admin/summary')
    summary.value = isSummary(value) ? value : null
  } catch (value: any) {
    summary.value = null
    error.value = value?.data?.error || value?.message || 'Unable to load administration summary'
  }
}

watch(manager.user, user => {
  if (user) void load()
}, { immediate: true })
</script>

<template>
  <AdminShell title="Dashboard" description="Security, provider and runtime status for the manager.">
    <template #actions><AppButton intent="secondary" @click="load">Refresh</AppButton></template>

    <UAlert v-if="error" class="mb-5" color="error" variant="subtle" :description="error" />

    <div v-if="summary" class="grid gap-4 lg:grid-cols-3" data-testid="admin-summary-cards">
      <NuxtLink to="/admin/users" class="block">
        <Frame class="h-full p-5 transition-colors hover:bg-[var(--neutral-100)]">
          <p class="text-sm font-semibold">Users</p>
          <p class="mt-1 text-xs text-[var(--neutral-700)]">Local management accounts</p>
          <p class="mt-6 font-[var(--font-heading)] text-[26px] font-semibold leading-none">{{ summary.users.enabled }} enabled</p>
          <p class="mt-1 font-mono text-[10.5px] text-[var(--neutral-700)]">{{ summary.users.total }} total</p>
        </Frame>
      </NuxtLink>

      <NuxtLink to="/admin/huggingface" class="block">
        <Frame class="h-full p-5 transition-colors hover:bg-[var(--neutral-100)]">
          <p class="text-sm font-semibold">Hugging Face</p>
          <p class="mt-1 text-xs text-[var(--neutral-700)]">Provider credential</p>
          <p class="mt-6 font-[var(--font-heading)] text-[26px] font-semibold leading-none">{{ summary.huggingface.configured ? 'Configured' : 'Not configured' }}</p>
          <p class="mt-1 font-mono text-[10.5px] text-[var(--neutral-700)]">{{ summary.huggingface.configured && summary.huggingface.prefix ? `${summary.huggingface.prefix}…` : 'No token' }}</p>
        </Frame>
      </NuxtLink>

      <NuxtLink to="/admin/llamacpp" class="block">
        <Frame class="h-full p-5 transition-colors hover:bg-[var(--neutral-100)]">
          <p class="text-sm font-semibold">llama.cpp</p>
          <p class="mt-1 text-xs text-[var(--neutral-700)]">Binary capabilities and defaults</p>
          <p class="mt-6 font-[var(--font-heading)] text-[26px] font-semibold leading-none">{{ summary.llamacpp.available ? (summary.llamacpp.version || 'Available') : 'Unavailable' }}</p>
          <p class="mt-1 font-mono text-[10.5px] text-[var(--neutral-700)]">{{ manager.profile.value?.options.length || 0 }} discovered options</p>
        </Frame>
      </NuxtLink>
    </div>
    <div v-else class="grid gap-4 md:grid-cols-3"><USkeleton v-for="n in 3" :key="n" class="h-40 w-full" /></div>
  </AdminShell>
</template>
