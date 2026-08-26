<script setup lang="ts">
const manager = useManager()
const { user, initialized, bootstrapRequired, backendError, apiBase } = manager
const credentials = reactive({ username: '', password: '' })
const authError = ref('')
const authenticating = ref(false)

const navigation = [
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
  <UMain v-if="!initialized" class="grid min-h-screen place-items-center px-6 py-10">
    <div class="grid justify-items-center gap-4 text-muted">
      <USkeleton class="size-8 rounded-full" />
      <p>Connecting to manager…</p>
    </div>
  </UMain>

  <UMain v-else-if="backendError" class="grid min-h-screen place-items-center px-6 py-10">
    <UCard class="w-full max-w-md bg-muted/90 shadow-2xl">
      <div class="space-y-5">
        <div class="text-4xl font-black text-primary">λ</div>
        <div>
          <p class="mb-2 text-xs font-extrabold tracking-[0.18em] text-dimmed">BACKEND UNAVAILABLE</p>
          <h1 class="text-3xl font-bold">Manager connection failed</h1>
        </div>
        <UAlert color="error" variant="subtle" :description="backendError" />
        <code class="block break-all font-mono text-sm text-error">{{ apiBase }}</code>
        <UButton color="primary" @click="manager.initialize">Retry</UButton>
      </div>
    </UCard>
  </UMain>

  <UMain v-else-if="!user" class="grid min-h-screen place-items-center px-6 py-10">
    <UCard class="w-full max-w-md bg-muted/90 shadow-2xl">
      <div class="text-4xl font-black text-primary">λ</div>
      <p class="mt-5 mb-2 text-xs font-extrabold tracking-[0.18em] text-dimmed">LLAMA.CPP CONTROL PLANE</p>
      <h1 class="text-3xl font-bold">{{ bootstrapRequired ? 'Create administrator' : 'Welcome back' }}</h1>
      <p class="mt-2 leading-6 text-muted">{{ bootstrapRequired ? 'Create the first local administrator account.' : 'Sign in to manage local inference.' }}</p>
      <UAlert v-if="authError" class="mt-5" color="error" variant="subtle" :description="authError" />

      <UForm :state="credentials" class="mt-6 space-y-4" @submit="submitAuth">
        <UFormField label="Username" required>
          <UInput v-model="credentials.username" class="w-full" autocomplete="username" required />
        </UFormField>
        <UFormField label="Password" required>
          <UInput
            v-model="credentials.password"
            class="w-full"
            type="password"
            :autocomplete="bootstrapRequired ? 'new-password' : 'current-password'"
            minlength="10"
            required
          />
        </UFormField>
        <UButton class="w-full justify-center" type="submit" :loading="authenticating">
          {{ bootstrapRequired ? 'Create admin' : 'Sign in' }}
        </UButton>
      </UForm>
    </UCard>
  </UMain>

  <UDashboardGroup v-else>
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
          <UUser :name="user.username" :description="user.role" size="sm" />
          <UButton
            data-testid="sign-out"
            class="text-button"
            color="neutral"
            variant="link"
            size="xs"
            @click="manager.logout"
          >
            Sign out
          </UButton>
        </div>
      </template>
    </UDashboardSidebar>

    <UDashboardPanel id="manager-main">
      <template #header>
        <UDashboardNavbar title="llamacpp-manager" class="lg:hidden">
          <template #leading>
            <UDashboardSidebarToggle />
          </template>
        </UDashboardNavbar>
      </template>
      <template #body>
        <div class="mx-auto w-full max-w-[1600px] p-4 sm:p-6 lg:p-10">
          <slot />
        </div>
      </template>
    </UDashboardPanel>
  </UDashboardGroup>
</template>
