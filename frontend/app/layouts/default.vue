<script setup lang="ts">
import AppSidebar from '~/components/navigation/AppSidebar.vue'

const route = useRoute()
const manager = useManager()
const { user, initialized, bootstrapRequired, backendError, apiBase, localLoginEnabled, authProviders } = manager
const credentials = reactive({ username: '', password: '' })
const remember = ref(false)
const authError = ref('')
const authenticating = ref(false)

onMounted(async () => {
  if (!initialized.value) await manager.initialize()
  const exchange = typeof route.query.oidc_exchange === 'string' ? route.query.oidc_exchange : ''
  if (exchange && !user.value) {
    authenticating.value = true
    try {
      await manager.exchangeOIDC(exchange)
      await navigateTo({ path: route.path, query: {} }, { replace: true })
    } catch (error: any) {
      authError.value = error?.data?.error || error?.message || 'External authentication failed'
    } finally {
      authenticating.value = false
    }
  }
})

async function submitAuth() {
  authenticating.value = true
  authError.value = ''
  try {
    await manager.authenticate(credentials.username, credentials.password, remember.value)
    credentials.password = ''
  } catch (error: any) {
    authError.value = error?.data?.error || error?.message || 'Authentication failed'
  } finally {
    authenticating.value = false
  }
}
</script>

<template>
  <UMain v-show="!initialized" class="fixed inset-0 z-50 grid min-h-screen place-items-center bg-default px-6 py-10">
    <div class="grid justify-items-center gap-4 text-muted">
      <USkeleton class="size-8 rounded-none" />
      <p>Connecting to manager…</p>
    </div>
  </UMain>

  <UMain v-show="initialized && !!backendError" class="fixed inset-0 z-50 grid min-h-screen place-items-center bg-default px-6 py-10">
    <UCard class="w-full max-w-md bg-muted/90 shadow-2xl">
      <div class="space-y-5">
        <div class="text-4xl font-black text-primary">λ</div>
        <div>
          <p class="mb-2 text-xs font-extrabold tracking-[0.18em] text-dimmed">BACKEND UNAVAILABLE</p>
          <h1 class="text-3xl font-bold">Manager connection failed</h1>
        </div>
        <div class="flex items-start gap-2 border border-[var(--color-divider)] px-3 py-2">
          <StatusTag variant="failed">Error</StatusTag>
          <p class="text-xs leading-5 text-[var(--neutral-800)]">{{ backendError }}</p>
        </div>
        <code class="block break-all font-mono text-sm text-error">{{ apiBase }}</code>
        <UButton color="primary" @click="manager.initialize">Retry</UButton>
      </div>
    </UCard>
  </UMain>

  <UMain v-show="initialized && !backendError && !user" class="fixed inset-0 z-40 grid min-h-screen place-items-center bg-default px-6 py-10">
    <UCard class="w-full max-w-md bg-muted/90 shadow-2xl">
      <div class="text-4xl font-black text-primary">λ</div>
      <p class="mt-5 mb-2 text-xs font-extrabold tracking-[0.18em] text-dimmed">LLAMA.CPP CONTROL PLANE</p>
      <h1 class="text-3xl font-bold">{{ bootstrapRequired ? 'Create account' : 'Welcome back' }}</h1>
      <p class="mt-2 leading-6 text-muted">{{ bootstrapRequired ? 'Create the first local management account.' : 'Sign in to manage local inference.' }}</p>
      <div v-if="authError" class="mt-5 flex items-start gap-2 border border-[var(--color-divider)] px-3 py-2">
        <StatusTag variant="failed">Error</StatusTag>
        <p class="text-xs leading-5 text-[var(--neutral-800)]">{{ authError }}</p>
      </div>

      <UForm v-if="bootstrapRequired || localLoginEnabled" :state="credentials" class="mt-6 space-y-4" @submit="submitAuth">
        <UFormField label="Username" name="username" required>
          <UInput v-model="credentials.username" class="w-full" autocomplete="username" required />
        </UFormField>
        <UFormField label="Password" name="password" required>
          <UInput v-model="credentials.password" class="w-full" type="password" :autocomplete="bootstrapRequired ? 'new-password' : 'current-password'" minlength="10" required />
        </UFormField>
        <UCheckbox v-if="!bootstrapRequired" v-model="remember" label="Remember me on this device" />
        <UButton class="w-full justify-center" type="submit" :loading="authenticating">
          {{ bootstrapRequired ? 'Create account' : 'Sign in' }}
        </UButton>
      </UForm>

      <template v-if="!bootstrapRequired && authProviders.length">
        <USeparator v-if="localLoginEnabled" class="my-6" label="or" />
        <div class="space-y-3">
          <UButton v-for="provider in authProviders" :key="provider.id" class="w-full justify-center" color="neutral" variant="outline" :disabled="authenticating" @click="manager.beginOIDC(provider.id, remember)">
            Continue with {{ provider.name }}
          </UButton>
          <UCheckbox v-if="!localLoginEnabled" v-model="remember" label="Remember me on this device" />
        </div>
      </template>
    </UCard>
  </UMain>

  <UDashboardGroup v-show="initialized && !backendError && !!user" class="relative inset-auto min-h-svh overflow-visible">
    <AppSidebar />

    <UDashboardPanel id="manager-main" :ui="{ body: 'overflow-visible' }">
      <template #header>
        <UDashboardNavbar title="llamacpp-manager" class="lg:hidden">
          <template #leading><UDashboardSidebarToggle /></template>
        </UDashboardNavbar>
      </template>
      <template #body>
        <div class="w-full p-4 sm:p-6 lg:p-10"><slot /></div>
      </template>
    </UDashboardPanel>
  </UDashboardGroup>
</template>
