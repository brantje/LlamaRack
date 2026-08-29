<script setup lang="ts">
import type { NavigationMenuItem } from '@nuxt/ui'
import ThemeSelector from '~/components/ThemeSelector.vue'

const manager = useManager()
const { user } = manager

const navigation: NavigationMenuItem[] = [
  { label: 'Overview', icon: 'i-lucide-layout-dashboard', to: '/' },
  { label: 'Models', icon: 'i-lucide-box', to: '/models' },
  { label: 'Instances', icon: 'i-lucide-server', to: '/instances' },
  { label: 'Downloads', icon: 'i-lucide-download', to: '/downloads' },
  { label: 'Logs', icon: 'i-lucide-list-start', to: '/logs' },
  { label: 'API', icon: 'i-lucide-key-round', to: '/api' }
]
const administrationNavigation: NavigationMenuItem[] = [
  { label: 'Administration', icon: 'i-lucide-settings-2', to: '/admin' }
]
</script>

<template>
  <UDashboardSidebar id="manager-sidebar" collapsible class="bg-[var(--neutral-100)]">
    <template #header>
      <UButton to="/" color="neutral" variant="link" class="h-auto justify-start gap-3 px-1 py-2">
        <span class="font-[var(--font-heading)] text-3xl font-semibold text-[var(--color-accent)]">λ</span>
        <span class="text-left font-[var(--font-heading)] text-sm font-semibold leading-[1.05] text-highlighted">llamacpp<br>manager</span>
      </UButton>
    </template>

    <UNavigationMenu :items="navigation" orientation="vertical" class="w-full" />

    <template #footer>
      <div class="grid w-full gap-3">
        <UNavigationMenu :items="administrationNavigation" orientation="vertical" class="mt-auto w-full" data-testid="administration-main-nav" />
        <ThemeSelector />
        <div class="flex w-full items-center justify-between gap-3">
          <UButton to="/profile" color="neutral" variant="ghost" class="min-w-0 justify-start px-1">
            <UUser :name="user?.username || ''" size="sm" />
          </UButton>
          <UButton data-testid="sign-out" color="neutral" variant="link" size="xs" @click="manager.logout">Sign out</UButton>
        </div>
      </div>
    </template>
  </UDashboardSidebar>
</template>
