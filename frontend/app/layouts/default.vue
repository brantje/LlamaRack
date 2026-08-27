<script setup lang="ts">
import AppSidebar from '~/components/navigation/AppSidebar.vue'
import AdminSidebar from '~/components/navigation/AdminSidebar.vue'

const route = useRoute()
const manager = useManager()
const { user, initialized, bootstrapRequired, backendError, apiBase } = manager
const credentials = reactive({ username: '', password: '' })
const authError = ref('')
const authenticating = ref(false)
const isAdmin = computed(() => route.path === '/admin' || route.path.startsWith('/admin/'))

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
  <UMain v-show="!initialized" class="fixed inset-0 z-50 grid min-h-screen place-items-center bg-default px-6 py-10">
    <div class="grid justify-items-center gap-4 text-muted">
      <USkeleton class="size-8 rounded-full" />
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
        <UAlert color="error" variant="subtle" :description="backendError" />
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
      <UAlert v-if="authError" class="mt-5" color="error" variant="subtle" :description="authError" />

      <UForm :state="credentials" class="mt-6 space-y-4" @submit="submitAuth">
        <UFormField label="Username" name="username" required>
          <UInput v-model="credentials.username" class="w-full" autocomplete="username" required />
        </UFormField>
        <UFormField label="Password" name="password" required>
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
          {{ bootstrapRequired ? 'Create account' : 'Sign in' }}
        </UButton>
      </UForm>
    </UCard>
  </UMain>

  <UDashboardGroup v-show="initialized && !backendError && !!user">
    <AdminSidebar v-if="isAdmin" />
    <AppSidebar v-else />

    <UDashboardPanel id="manager-main">
      <template #header>
        <UDashboardNavbar :title="isAdmin ? 'Administration' : 'llamacpp-manager'" class="lg:hidden">
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
