<script setup lang="ts">
import type { NavigationMenuItem } from '@nuxt/ui'

const manager = useManager()
const { user } = manager

const navigation: NavigationMenuItem[] = [
  { label: 'Overview', icon: 'i-lucide-layout-dashboard', to: '/' },
  { label: 'Models', icon: 'i-lucide-box', to: '/models' },
  { label: 'Instances', icon: 'i-lucide-server', to: '/instances' },
  { label: 'Downloads', icon: 'i-lucide-download', to: '/downloads' },
  { label: 'API', icon: 'i-lucide-key-round', to: '/api' },
  { type: 'separator' },
  { label: 'Administration', icon: 'i-lucide-settings-2', to: '/admin' }
]
</script>

<template>
  <UDashboardSidebar id="manager-sidebar" collapsible>
    <template #header>
      <UButton to="/" color="neutral" variant="link" class="h-auto justify-start gap-3 px-1 py-2">
        <span class="text-3xl font-black text-primary">λ</span>
        <span class="text-left text-sm font-extrabold leading-[1.05] text-highlighted">llamacpp<br>manager</span>
      </UButton>
    </template>

    <UNavigationMenu :items="navigation" orientation="vertical" class="w-full" />

    <template #footer>
      <div class="flex w-full items-center justify-between gap-3">
        <UButton to="/profile" color="neutral" variant="ghost" class="min-w-0 justify-start px-1">
          <UUser :name="user?.username || ''" size="sm" />
        </UButton>
        <UButton data-testid="sign-out" color="neutral" variant="link" size="xs" @click="manager.logout">Sign out</UButton>
      </div>
    </template>
  </UDashboardSidebar>
</template>
