<script setup lang="ts">
import type { NavigationMenuItem } from '@nuxt/ui'

const route = useRoute()

const navigationMenuUi = {
  label: 'mb-3 px-3 text-[length:var(--font-size-kicker)] font-extrabold tracking-[0.18em] text-[var(--neutral-700)]',
  link: 'rounded-none',
  separator: 'my-3'
}

function navTestId(prefix: string, label: string) {
  return `${prefix}-${label.toLowerCase().replaceAll(' ', '-')}`
}

function buildItems(testIdPrefix: string): NavigationMenuItem[][] {
  const testId = (label: string) => navTestId(testIdPrefix, label)

  const groups: NavigationMenuItem[][] = [
    [
      { label: 'Administration', type: 'label' },
      { label: 'Dashboard', to: '/admin', exact: true, 'data-testid': testId('Dashboard') },
      { label: 'General', to: '/admin/general', 'data-testid': testId('General') }
    ],
    [
      { label: 'Access', type: 'label' },
      { label: 'Users', to: '/admin/users', 'data-testid': testId('Users') },
      { label: 'Service accounts', to: '/admin/service-accounts', 'data-testid': testId('Service accounts') },
      { label: 'Authentication', to: '/admin/authentication', 'data-testid': testId('Authentication') }
    ],
    [
      { label: 'Runtime & integrations', type: 'label' },
      { label: 'llama.cpp', to: '/admin/llamacpp', 'data-testid': testId('llama.cpp') },
      { label: 'Hugging Face', to: '/admin/huggingface', 'data-testid': testId('Hugging Face') },
      { label: 'LiteLLM', to: '/admin/litellm', 'data-testid': testId('LiteLLM') }
    ],
    [
      { label: 'Diagnostics', type: 'label' },
      { label: 'System', to: '/admin/system', 'data-testid': testId('System') },
      { label: 'Logs', to: '/admin/system-logs', 'data-testid': testId('Logs') }
    ]
  ]

  return groups.map(group => group.map(withActiveState))
}

function withActiveState(item: NavigationMenuItem): NavigationMenuItem {
  const to = itemPath(item)
  if (!to) {
    return item
  }
  const isActive = active(to, item.exact === true)
  return {
    ...item,
    active: isActive,
    ...(isActive ? { 'aria-current': 'page' } : {})
  }
}

const desktopItems = computed(() => buildItems('admin-nav'))
const mobileItems = computed(() => buildItems('admin-mobile-nav'))

function itemPath(item: NavigationMenuItem) {
  return typeof item.to === 'string' ? item.to : undefined
}

function active(to: string, exact = false) {
  return exact ? route.path === to : route.path === to || route.path.startsWith(`${to}/`)
}

const currentItem = computed(() => {
  for (const group of desktopItems.value) {
    for (const item of group) {
      const to = itemPath(item)
      if (to && active(to, item.exact === true)) {
        return item
      }
    }
  }
  return desktopItems.value[0]![1]
})
</script>

<template>
  <aside class="w-full shrink-0 border-b border-[var(--color-divider)] pb-4 lg:w-[216px] lg:border-r lg:border-b-0 lg:pr-4 lg:pb-0" data-testid="admin-secondary-nav">
    <UCollapsible class="space-y-2 lg:hidden" data-testid="admin-mobile-navigation">
      <template #default="{ open }">
        <UButton type="button" color="neutral" variant="outline" class="min-h-11 w-full rounded-none">
          <span class="flex w-full items-center justify-between gap-3 text-left">
            <span class="min-w-0 text-[length:var(--font-size-table-body)] font-semibold leading-5">Administration · {{ currentItem.label }}</span>
            <UIcon :name="open ? 'i-lucide-chevron-up' : 'i-lucide-chevron-down'" class="size-4 shrink-0 text-[var(--neutral-700)]" aria-hidden="true" />
          </span>
        </UButton>
      </template>

      <template #content>
        <UNavigationMenu
          :items="mobileItems"
          orientation="vertical"
          class="w-full border border-[var(--color-divider)]"
          :ui="navigationMenuUi"
          aria-label="Administration sections"
        />
      </template>
    </UCollapsible>

    <div class="hidden lg:block" data-testid="admin-desktop-navigation">
      <UNavigationMenu
        :items="desktopItems"
        orientation="vertical"
        class="w-full"
        :ui="navigationMenuUi"
        aria-label="Administration"
      />
    </div>
  </aside>
</template>
