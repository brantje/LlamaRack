<script setup lang="ts">
const route = useRoute()

const navigation = [
  { label: 'Dashboard', description: 'Security, provider and runtime status', to: '/admin' },
  { label: 'General', description: 'Security, network and lifecycle defaults', to: '/admin/general' },
  { label: 'Authentication', description: 'Local login and OIDC providers', to: '/admin/authentication' },
  { label: 'llama.cpp', description: 'Binary capabilities and global defaults', to: '/admin/llamacpp' },
  { label: 'Hugging Face', description: 'Provider credential', to: '/admin/huggingface' },
  { label: 'Users', description: 'Local management accounts', to: '/admin/users' },
  { label: 'System', description: 'Read-only diagnostics', to: '/admin/system' },
  { label: 'Logs', description: 'Manager, gateway and Instance diagnostics', to: '/admin/system-logs' }
]

function active(to: string) {
  return to === '/admin' ? route.path === '/admin' : route.path === to || route.path.startsWith(`${to}/`)
}
</script>

<template>
  <aside class="w-full shrink-0 border-b border-[var(--color-divider)] pb-4 lg:w-[216px] lg:border-r lg:border-b-0 lg:pr-4 lg:pb-0" data-testid="admin-secondary-nav">
    <p class="mb-3 px-3 text-[9.5px] font-extrabold tracking-[0.18em] text-[var(--neutral-700)]">Administration</p>
    <nav class="space-y-1" aria-label="Administration">
      <NuxtLink
        v-for="item in navigation"
        :key="item.to"
        :to="item.to"
        class="block border-l-2 px-3 py-2 transition-colors"
        :class="active(item.to)
          ? 'border-[var(--color-accent)] bg-[var(--accent-100)] text-[var(--accent-800)]'
          : 'border-transparent bg-transparent text-[var(--neutral-900)] hover:bg-[var(--neutral-100)]'"
        :data-testid="`admin-nav-${item.label.toLowerCase().replaceAll(' ', '-')}`"
      >
        <span class="block text-[13.5px] font-semibold leading-5">{{ item.label }}</span>
        <span class="block text-[10.5px] leading-4" :class="active(item.to) ? 'text-[var(--accent-700)]' : 'text-[var(--neutral-700)]'">{{ item.description }}</span>
      </NuxtLink>
    </nav>
  </aside>
</template>
