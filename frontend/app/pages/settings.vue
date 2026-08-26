<script setup lang="ts">
const manager = useManager()
const { apiBase, profile } = manager
</script>

<template>
  <div class="space-y-5">
    <div class="flex items-start justify-between gap-6">
      <UPageHeader
        class="min-w-0 flex-1"
        headline="RUNTIME"
        title="Settings"
        description="Detected backend and llama.cpp capabilities."
      />
      <UButton color="neutral" variant="soft" @click="manager.refresh">Refresh</UButton>
    </div>

    <UCard>
      <p class="mb-1 text-xs font-extrabold tracking-[0.18em] text-dimmed">BACKEND</p>
      <h2 class="text-xl font-bold">Connection</h2>
      <dl class="mt-4 divide-y divide-default text-sm">
        <div class="grid gap-1 py-3 sm:grid-cols-[170px_1fr] sm:gap-5">
          <dt class="text-muted">Management API</dt>
          <dd><code class="break-all font-mono">{{ apiBase }}/api/v1</code></dd>
        </div>
        <div class="grid gap-1 py-3 sm:grid-cols-[170px_1fr] sm:gap-5">
          <dt class="text-muted">OpenAI endpoint</dt>
          <dd><code class="break-all font-mono">{{ apiBase }}/v1</code></dd>
        </div>
      </dl>
    </UCard>

    <UCard>
      <p class="mb-1 text-xs font-extrabold tracking-[0.18em] text-dimmed">LLAMA.CPP</p>
      <h2 class="text-xl font-bold">Binary capabilities</h2>
      <dl v-if="profile" class="mt-4 divide-y divide-default text-sm">
        <div class="grid gap-1 py-3 sm:grid-cols-[170px_1fr] sm:gap-5">
          <dt class="text-muted">Binary</dt>
          <dd><code class="break-all font-mono">{{ profile.path }}</code></dd>
        </div>
        <div class="grid gap-1 py-3 sm:grid-cols-[170px_1fr] sm:gap-5">
          <dt class="text-muted">Version</dt>
          <dd><code class="font-mono">{{ profile.version || 'unknown' }}</code></dd>
        </div>
        <div class="grid gap-1 py-3 sm:grid-cols-[170px_1fr] sm:gap-5">
          <dt class="text-muted">Fingerprint</dt>
          <dd><code class="break-all font-mono">{{ profile.fingerprint.slice(0, 24) }}…</code></dd>
        </div>
        <div class="grid gap-1 py-3 sm:grid-cols-[170px_1fr] sm:gap-5">
          <dt class="text-muted">Discovered options</dt>
          <dd class="font-semibold">{{ profile.options.length }}</dd>
        </div>
      </dl>
      <UAlert
        v-else
        class="mt-4"
        color="warning"
        variant="subtle"
        description="llama-server could not be discovered. Management features still work, but model workers cannot start until the binary path is correct."
      />
    </UCard>
  </div>
</template>
