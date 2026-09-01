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

const currentItem = computed(() => navigation.find(item => active(item.to)) ?? navigation[0])
</script>

<template>
  <aside class="w-full shrink-0 border-b border-[var(--color-divider)] pb-4 lg:w-[216px] lg:border-r lg:border-b-0 lg:pr-4 lg:pb-0" data-testid="admin-secondary-nav">
    <details class="lg:hidden" data-testid="admin-mobile-navigation">
      <summary class="flex min-h-11 cursor-pointer items-center justify-between border border-[var(--color-divider)] px-3 py-2 text-[var(--neutral-900)] focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--color-accent)]">
        <span class="min-w-0">
          <span class="block text-[length:var(--font-size-table-body)] font-semibold leading-5">Administration · {{ currentItem.label }}</span>
          <span class="block text-[length:var(--font-size-table-header)] leading-4 text-[var(--neutral-800)]">Choose administration section</span>
        </span>
        <UIcon name="i-lucide-chevron-down" class="size-4 shrink-0 text-[var(--neutral-700)]" aria-hidden="true" />
      </summary>
      <nav class="mt-2 border border-[var(--color-divider)]" aria-label="Administration sections">
        <NuxtLink
          v-for="item in navigation"
          :key="item.to"
          :to="item.to"
          class="block border-l-2 px-3 py-2.5 transition-colors"
          :class="active(item.to)
            ? 'border-[var(--color-accent)] bg-[var(--accent-100)] text-[var(--accent-800)]'
            : 'border-transparent bg-transparent text-[var(--neutral-900)] hover:bg-[var(--neutral-100)]'"
          :aria-current="active(item.to) ? 'page' : undefined"
          :data-testid="`admin-mobile-nav-${item.label.toLowerCase().replaceAll(' ', '-')}`"
        >
          <span class="block text-[length:var(--font-size-table-body)] font-semibold leading-5">{{ item.label }}</span>
          <span class="block text-[length:var(--font-size-table-header)] leading-4" :class="active(item.to) ? 'text-[var(--accent-700)]' : 'text-[var(--neutral-800)]'">{{ item.description }}</span>
        </NuxtLink>
      </nav>
    </details>

    <div class="hidden lg:block" data-testid="admin-desktop-navigation">
      <p class="mb-3 px-3 text-[length:var(--font-size-kicker)] font-extrabold tracking-[0.18em] text-[var(--neutral-700)]">Administration</p>
      <nav class="space-y-1" aria-label="Administration">
        <NuxtLink
          v-for="item in navigation"
          :key="item.to"
          :to="item.to"
          class="block border-l-2 px-3 py-2 transition-colors"
          :class="active(item.to)
            ? 'border-[var(--color-accent)] bg-[var(--accent-100)] text-[var(--accent-800)]'
            : 'border-transparent bg-transparent text-[var(--neutral-900)] hover:bg-[var(--neutral-100)]'"
          :aria-current="active(item.to) ? 'page' : undefined"
          :data-testid="`admin-nav-${item.label.toLowerCase().replaceAll(' ', '-')}`"
        >
          <span class="block text-[length:var(--font-size-table-body)] font-semibold leading-5">{{ item.label }}</span>
          <span class="block text-[length:var(--font-size-table-header)] leading-4" :class="active(item.to) ? 'text-[var(--accent-700)]' : 'text-[var(--neutral-800)]'">{{ item.description }}</span>
        </NuxtLink>
      </nav>
    </div>
  </aside>
</template>
