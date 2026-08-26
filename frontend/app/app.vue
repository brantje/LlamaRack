<script setup lang="ts">
const manager = useManager()
const { user, initialized, bootstrapRequired, backendError, apiBase } = manager
const credentials = reactive({ username: '', password: '' })
const authError = ref('')
const authenticating = ref(false)

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
  <div v-if="!initialized" class="center-screen"><div class="spinner" /><p>Connecting to manager…</p></div>
  <div v-else-if="backendError" class="center-screen"><section class="auth-card error-card"><div class="brand-mark">λ</div><p class="eyebrow">BACKEND UNAVAILABLE</p><h1>Manager connection failed</h1><p>{{ backendError }}</p><code>{{ apiBase }}</code><button class="primary" @click="manager.initialize">Retry</button></section></div>
  <main v-else-if="!user" class="auth-shell"><section class="auth-card"><div class="brand-mark">λ</div><p class="eyebrow">LLAMA.CPP CONTROL PLANE</p><h1>{{ bootstrapRequired ? 'Create administrator' : 'Welcome back' }}</h1><p class="muted">{{ bootstrapRequired ? 'Create the first local administrator account.' : 'Sign in to manage local inference.' }}</p><p v-if="authError" class="alert error">{{ authError }}</p><form @submit.prevent="submitAuth"><label>Username<input v-model="credentials.username" autocomplete="username" required></label><label>Password<input v-model="credentials.password" type="password" :autocomplete="bootstrapRequired ? 'new-password' : 'current-password'" minlength="10" required></label><button class="primary" :disabled="authenticating">{{ authenticating ? 'Working…' : bootstrapRequired ? 'Create admin' : 'Sign in' }}</button></form></section></main>
  <div v-else class="app-shell"><aside class="sidebar"><NuxtLink to="/" class="brand"><span>λ</span><strong>llamacpp<br>manager</strong></NuxtLink><nav><NuxtLink to="/">Overview</NuxtLink><NuxtLink to="/models">Models</NuxtLink><NuxtLink to="/api">API</NuxtLink><NuxtLink to="/settings">Settings</NuxtLink></nav><div class="identity"><div><strong>{{ user.username }}</strong><small>{{ user.role }}</small></div><button class="text-button" @click="manager.logout">Sign out</button></div></aside><main class="main-content"><NuxtPage /></main></div>
</template>
