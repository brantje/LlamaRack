<script setup lang="ts">
import type { NavigationMenuItem } from '@nuxt/ui'

const manager = useManager()
const { user, initialized, bootstrapRequired, backendError, apiBase } = manager
const credentials = reactive({ username: '', password: '' })
const authError = ref('')
const authenticating = ref(false)

const navigation: NavigationMenuItem[] = [
  { label: 'Overview', to: '/' },
  { label: 'Models', to: '/models' },
  { label: 'API', to: '/api' },
  { label: 'Settings', to: '/settings' }
]

onMounted(() => {
  if (!initialized.value) manager.initialize()
})

async function submitAuth() {
  authenticating.value = true
  authError.value = ''
  try {
    await manager.authenticate(credentials.username, credentials.password)
    credentials.password = ''
  } catch (error: any) {
    authError.value = error?.data?.error || error?.message || 'Authentication failed'
  } finally {
    authenticating.value = false
  }
}
</script>

<template>
  <div v-if="!initialized" class="grid min-h-screen place-items-center px-6">
    <div class="grid w-full max-w-xs gap-4 text-center text-muted">
      <USkeleton class="mx-auto h-2 w-40 rounded-full" />
      <p>Connecting to manager…</p>
    </div>
  </div>

  <div v-else-if="backendError" class="grid min-h-screen place-items-center px-6 py-10">
    <UCard class="w-full max-w-md shadow-2xl shadow-black/50">
      <div class="grid gap-5">
        <div>
          <div class="text-4xl font-black text-primary">λ</div>
          <p class="mt-2 text-[11px] font-extrabold tracking-[0.18em] text-muted">BACKEND UNAVAILABLE</p>
          <h1 class="mt-2 text-3xl font-bold text-highlighted">Manager connection failed</h1>
        </div>
        <UAlert color="error" variant="subtle" :description="backendError" />
        <code class="[overflow-wrap:anywhere] rounded-lg border border-default bg-default p-3 text-sm text-error">{{ apiBase }}</code>
        <UButton label="Retry" block :loading="!initialized" @click="manager.initialize" />
      </div>
    </UCard>
  </div>

  <main v-else-if="!user" class="grid min-h-screen place-items-center px-6 py-10">
    <UCard class="w-full max-w-md shadow-2xl shadow-black/50">
      <div class="mb-6">
        <div class="text-4xl font-black text-primary">λ</div>
        <p class="mt-2 text-[11px] font-extrabold tracking-[0.18em] text-muted">LLAMA.CPP CONTROL PLANE</p>
        <h1 class="mt-2 text-3xl font-bold text-highlighted">{{ bootstrapRequired ? 'Create administrator' : 'Welcome back' }}</h1>
        <p class="mt-3 text-sm leading-6 text-muted">{{ bootstrapRequired ? 'Create the first local administrator account.' : 'Sign in to manage local inference.' }}</p>
      </div>

      <UAlert v-if="authError" class="mb-5" color="error" variant="subtle" :description="authError" />

      <UForm :state="credentials" class="grid gap-4" @submit="submitAuth">
        <UFormField label="Username" name="username" required>
          <UInput v-model="credentials.username" autocomplete="username" required class="w-full" />
        </UFormField>
        <UFormField label="Password" name="password" required>
          <UInput
            v-model="credentials.password"
            type="password"
            :autocomplete="bootstrapRequired ? 'new-password' : 'current-password'"
            :minlength="10"
            required
            class="w-full"
          />
        </UFormField>
        <UButton
          type="submit"
          :label="bootstrapRequired ? 'Create admin' : 'Sign in'"
          :loading="authenticating"
          block
        />
      </UForm>
    </UCard>
  </main>

  <UDashboardGroup v-else class="min-h-screen">
    <UDashboardSidebar class="border-default bg-muted/95">
      <template #header>
        <ULink to="/" class="flex items-start gap-2 px-2 py-1 text-highlighted">
          <span class="text-3xl font-black leading-none text-primary">λ</span>
          <strong class="text-sm leading-[1.05]">llamacpp<br>manager</strong>
        </ULink>
      </template>

      <UNavigationMenu :items="navigation" orientation="vertical" class="w-full" />

      <template #footer>
        <div class="grid gap-3 border-t border-muted px-2 pt-4">
          <div class="grid">
            <strong class="text-sm text-highlighted">{{ user.username }}</strong>
            <span class="text-[10px] uppercase tracking-[0.12em] text-dimmed">{{ user.role }}</span>
          </div>
          <UButton label="Sign out" color="neutral" variant="ghost" size="sm" block @click="manager.logout" />
        </div>
      </template>
    </UDashboardSidebar>

    <UDashboardPanel class="min-w-0" :ui="{ body: 'p-0 sm:p-0' }">
      <UDashboardNavbar title="llamacpp-manager" class="lg:hidden" />

      <template #body>
        <div class="px-4 py-6 sm:px-6 sm:py-8 lg:px-10 xl:px-14">
          <slot />
        </div>
      </template>
    </UDashboardPanel>
  </UDashboardGroup>
</template>
