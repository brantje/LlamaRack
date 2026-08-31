<script setup lang="ts">
const route = useRoute()

const navigation = [
  { label: 'Account', description: 'Local account details and password', to: '/profile/account' },
  { label: 'Authentication', description: 'External sign-in providers linked to this account', to: '/profile/authentication' },
  { label: 'Sessions', description: 'Active management sessions', to: '/profile/sessions' }
]

function active(to: string) {
  return route.path === to || route.path.startsWith(`${to}/`)
}

const currentItem = computed(() => navigation.find(item => active(item.to)) ?? navigation[0])
</script>

<template>
  <aside class="w-full shrink-0 border-b border-[var(--color-divider)] pb-4 lg:w-[216px] lg:border-r lg:border-b-0 lg:pr-4 lg:pb-0" data-testid="profile-secondary-nav">
    <details class="lg:hidden" data-testid="profile-mobile-navigation">
      <summary class="flex min-h-11 cursor-pointer items-center justify-between border border-[var(--color-divider)] px-3 py-2 text-[var(--neutral-900)] focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-[var(--color-accent)]">
        <span class="min-w-0">
          <span class="block text-[13.5px] font-semibold leading-5">Account · {{ currentItem.label }}</span>
          <span class="block text-[11px] leading-4 text-[var(--neutral-800)]">Choose account section</span>
        </span>
        <UIcon name="i-lucide-chevron-down" class="size-4 shrink-0 text-[var(--neutral-700)]" aria-hidden="true" />
      </summary>
      <nav class="mt-2 border border-[var(--color-divider)]" aria-label="Account sections">
        <NuxtLink
          v-for="item in navigation"
          :key="item.to"
          :to="item.to"
          class="block border-l-2 px-3 py-2.5 transition-colors"
          :class="active(item.to)
            ? 'border-[var(--color-accent)] bg-[var(--accent-100)] text-[var(--accent-800)]'
            : 'border-transparent bg-transparent text-[var(--neutral-900)] hover:bg-[var(--neutral-100)]'"
          :aria-current="active(item.to) ? 'page' : undefined"
          :data-testid="`profile-mobile-nav-${item.label.toLowerCase().replaceAll(' ', '-')}`"
        >
          <span class="block text-[13.5px] font-semibold leading-5">{{ item.label }}</span>
          <span class="block text-[11px] leading-4" :class="active(item.to) ? 'text-[var(--accent-700)]' : 'text-[var(--neutral-800)]'">{{ item.description }}</span>
        </NuxtLink>
      </nav>
    </details>

    <div class="hidden lg:block" data-testid="profile-desktop-navigation">
      <p class="mb-3 px-3 text-[9.5px] font-extrabold tracking-[0.18em] text-[var(--neutral-700)]">Account</p>
      <nav class="space-y-1" aria-label="Account">
        <NuxtLink
          v-for="item in navigation"
          :key="item.to"
          :to="item.to"
          class="block border-l-2 px-3 py-2 transition-colors"
          :class="active(item.to)
            ? 'border-[var(--color-accent)] bg-[var(--accent-100)] text-[var(--accent-800)]'
            : 'border-transparent bg-transparent text-[var(--neutral-900)] hover:bg-[var(--neutral-100)]'"
          :aria-current="active(item.to) ? 'page' : undefined"
          :data-testid="`profile-nav-${item.label.toLowerCase().replaceAll(' ', '-')}`"
        >
          <span class="block text-[13.5px] font-semibold leading-5">{{ item.label }}</span>
          <span class="block text-[10.5px] leading-4" :class="active(item.to) ? 'text-[var(--accent-700)]' : 'text-[var(--neutral-800)]'">{{ item.description }}</span>
        </NuxtLink>
      </nav>
    </div>
  </aside>
</template>
