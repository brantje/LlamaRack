<script setup lang="ts">
const manager = useManager()
const { apiBase, profile } = manager
</script>

<template>
  <div class="space-y-4">
    <UPageHeader
      headline="RUNTIME"
      title="Settings"
      description="Detected backend and llama.cpp capabilities."
      class="mb-7"
    >
      <template #links>
        <UButton color="neutral" variant="outline" icon="i-lucide-refresh-cw" @click="manager.refresh">Refresh</UButton>
      </template>
    </UPageHeader>

    <UCard>
      <template #header>
        <div>
          <p class="mb-2 text-[11px] font-extrabold tracking-[0.18em] text-muted">BACKEND</p>
          <h2 class="text-xl font-bold text-highlighted">Connection</h2>
        </div>
      </template>
      <dl class="divide-y divide-default">
        <div class="grid gap-1 py-3 first:pt-0 last:pb-0 sm:grid-cols-[170px_1fr] sm:gap-5">
          <dt class="text-sm text-muted">Management API</dt>
          <dd><code class="text-sm [overflow-wrap:anywhere]">{{ apiBase }}/api/v1</code></dd>
        </div>
        <div class="grid gap-1 py-3 first:pt-0 last:pb-0 sm:grid-cols-[170px_1fr] sm:gap-5">
          <dt class="text-sm text-muted">OpenAI endpoint</dt>
          <dd><code class="text-sm [overflow-wrap:anywhere]">{{ apiBase }}/v1</code></dd>
        </div>
      </dl>
    </UCard>

    <UCard>
      <template #header>
        <div>
          <p class="mb-2 text-[11px] font-extrabold tracking-[0.18em] text-muted">LLAMA.CPP</p>
          <h2 class="text-xl font-bold text-highlighted">Binary capabilities</h2>
        </div>
      </template>

      <dl v-if="profile" class="divide-y divide-default">
        <div class="grid gap-1 py-3 first:pt-0 sm:grid-cols-[170px_1fr] sm:gap-5">
          <dt class="text-sm text-muted">Binary</dt>
          <dd><code class="text-sm [overflow-wrap:anywhere]">{{ profile.path }}</code></dd>
        </div>
        <div class="grid gap-1 py-3 sm:grid-cols-[170px_1fr] sm:gap-5">
          <dt class="text-sm text-muted">Version</dt>
          <dd><code class="text-sm">{{ profile.version || 'unknown' }}</code></dd>
        </div>
        <div class="grid gap-1 py-3 sm:grid-cols-[170px_1fr] sm:gap-5">
          <dt class="text-sm text-muted">Fingerprint</dt>
          <dd><code class="text-sm [overflow-wrap:anywhere]">{{ profile.fingerprint.slice(0, 24) }}…</code></dd>
        </div>
        <div class="grid gap-1 py-3 last:pb-0 sm:grid-cols-[170px_1fr] sm:gap-5">
          <dt class="text-sm text-muted">Discovered options</dt>
          <dd><strong class="text-sm text-highlighted">{{ profile.options.length }}</strong></dd>
        </div>
      </dl>
      <UAlert
        v-else
        color="warning"
        variant="subtle"
        icon="i-lucide-triangle-alert"
        title="llama-server could not be discovered"
        description="Management features still work, but model workers cannot start until the binary path is correct."
      />
    </UCard>
  </div>
</template>
