<script setup lang="ts">
import type { NavigationMenuItem } from '@nuxt/ui'

const manager = useManager()
const { user } = manager

const navigation: NavigationMenuItem[] = [
  { label: 'Dashboard', icon: 'i-lucide-gauge', to: '/admin' },
  { label: 'Users', icon: 'i-lucide-users', to: '/admin/users' },
  { label: 'Hugging Face', icon: 'i-lucide-sparkles', to: '/admin/huggingface' },
  { label: 'General', icon: 'i-lucide-sliders-horizontal', to: '/admin/general' },
  { label: 'llama.cpp', icon: 'i-lucide-terminal-square', to: '/admin/llamacpp' },
  { label: 'System', icon: 'i-lucide-activity', to: '/admin/system' },
  { type: 'separator' },
  { label: 'Back to manager', icon: 'i-lucide-arrow-left', to: '/' }
]
</script>

<template>
  <UDashboardSidebar id="admin-sidebar" collapsible>
    <template #header>
      <UButton to="/admin" color="neutral" variant="link" class="h-auto justify-start gap-3 px-1 py-2">
        <span class="text-3xl font-black text-primary">λ</span>
        <span class="text-left text-sm font-extrabold leading-[1.05] text-highlighted">administration<br>console</span>
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
