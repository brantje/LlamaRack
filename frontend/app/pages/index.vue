<script setup lang="ts">
type BackendInfo = {
  name?: string
  status?: string
}

const config = useRuntimeConfig()
const apiBase = computed(() => String(config.public.apiBase).replace(/\/$/, ''))
const backendOnline = ref(false)
const backendInfo = ref<BackendInfo | null>(null)
const checking = ref(false)
const lastChecked = ref<Date | null>(null)

async function checkBackend() {
  checking.value = true
  try {
    await $fetch(`${apiBase.value}/health`)
    backendInfo.value = await $fetch<BackendInfo>(`${apiBase.value}/`)
    backendOnline.value = true
  } catch {
    backendOnline.value = false
    backendInfo.value = null
  } finally {
    lastChecked.value = new Date()
    checking.value = false
  }
}

onMounted(() => {
  void checkBackend()
})
</script>

<template>
  <div class="shell">
    <aside class="sidebar">
      <div class="brand">
        <div class="brand-mark">λ</div>
        <div>
          <strong>llamacpp</strong>
          <span>manager</span>
        </div>
      </div>

      <nav>
        <button class="nav-item active">
          <span>Overview</span>
        </button>
        <button class="nav-item" disabled>
          <span>Models</span>
          <small>soon</small>
        </button>
        <button class="nav-item" disabled>
          <span>Discover</span>
          <small>soon</small>
        </button>
        <button class="nav-item" disabled>
          <span>Downloads</span>
          <small>soon</small>
        </button>
        <button class="nav-item" disabled>
          <span>API</span>
          <small>soon</small>
        </button>
        <button class="nav-item" disabled>
          <span>Settings</span>
          <small>soon</small>
        </button>
      </nav>

      <div class="sidebar-footer">
        <span class="status-dot" :class="backendOnline ? 'online' : 'offline'" />
        <div>
          <strong>{{ backendOnline ? 'Backend online' : 'Backend offline' }}</strong>
          <small>{{ apiBase }}</small>
        </div>
      </div>
    </aside>

    <main class="content">
      <header class="page-header">
        <div>
          <p class="eyebrow">LOCAL INFERENCE CONTROL PLANE</p>
          <h1>Overview</h1>
          <p class="subtitle">Manage llama.cpp models, workers, downloads and OpenAI-compatible routing.</p>
        </div>
        <button class="refresh" :disabled="checking" @click="checkBackend">
          {{ checking ? 'Checking…' : 'Check backend' }}
        </button>
      </header>

      <section v-if="!backendOnline" class="notice danger">
        <div>
          <strong>Backend is not reachable</strong>
          <p>The frontend is running, but it cannot reach <code>{{ apiBase }}</code>.</p>
        </div>
        <code>docker compose ps</code>
      </section>

      <section v-else class="notice success">
        <div>
          <strong>Development stack is connected</strong>
          <p>Nuxt is talking to the Go backend successfully.</p>
        </div>
        <span>{{ backendInfo?.name || 'llamacpp-manager-backend' }}</span>
      </section>

      <section class="stats">
        <article class="stat-card">
          <span>Configured models</span>
          <strong>0</strong>
          <small>Model registry is next</small>
        </article>
        <article class="stat-card">
          <span>Ready workers</span>
          <strong>0</strong>
          <small>No llama.cpp workers yet</small>
        </article>
        <article class="stat-card">
          <span>Active requests</span>
          <strong>0</strong>
          <small>OpenAI gateway not enabled yet</small>
        </article>
        <article class="stat-card">
          <span>Backend</span>
          <strong class="text-value">{{ backendOnline ? 'Healthy' : 'Offline' }}</strong>
          <small v-if="lastChecked">Checked {{ lastChecked.toLocaleTimeString() }}</small>
        </article>
      </section>

      <div class="grid">
        <section class="panel">
          <div class="panel-heading">
            <div>
              <p class="eyebrow">MODEL FLEET</p>
              <h2>Models</h2>
            </div>
            <button disabled>Add model</button>
          </div>
          <div class="empty-state">
            <div class="empty-icon">◇</div>
            <h3>No models configured</h3>
            <p>The next implementation step will add GGUF registration, model configuration and worker lifecycle controls.</p>
          </div>
        </section>

        <section class="panel side-panel">
          <p class="eyebrow">DEVELOPMENT</p>
          <h2>Local endpoints</h2>
          <dl>
            <div>
              <dt>Frontend</dt>
              <dd><code>http://localhost:3000</code></dd>
            </div>
            <div>
              <dt>Backend</dt>
              <dd><code>{{ apiBase }}</code></dd>
            </div>
            <div>
              <dt>Health</dt>
              <dd><code>{{ apiBase }}/health</code></dd>
            </div>
            <div>
              <dt>Container port</dt>
              <dd><code>8000 → 8888</code></dd>
            </div>
          </dl>
        </section>
      </div>
    </main>
  </div>
</template>

<style scoped>
:global(*) { box-sizing: border-box; }
:global(html) { background: #090d12; color-scheme: dark; }
:global(body) { margin: 0; min-width: 320px; font-family: Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; background: #090d12; color: #e8edf4; }
:global(button), :global(code) { font: inherit; }

.shell { min-height: 100vh; display: grid; grid-template-columns: 240px 1fr; background: radial-gradient(circle at 80% 0%, #14212a 0, #090d12 34%); }
.sidebar { position: sticky; top: 0; height: 100vh; padding: 24px 16px; border-right: 1px solid #202a35; background: #0d1218e8; display: flex; flex-direction: column; }
.brand { display: flex; align-items: center; gap: 12px; padding: 6px 8px 28px; }
.brand-mark { width: 38px; height: 38px; display: grid; place-items: center; border: 1px solid #294638; border-radius: 10px; color: #8bf0c5; font-size: 24px; font-weight: 800; background: #10251e; }
.brand strong, .brand span { display: block; line-height: 1.05; }
.brand span { color: #8796a6; font-size: 13px; }
nav { display: grid; gap: 5px; }
.nav-item { width: 100%; border: 0; border-radius: 8px; padding: 10px 12px; background: transparent; color: #8e9cad; text-align: left; display: flex; justify-content: space-between; align-items: center; }
.nav-item.active { color: #eef5f8; background: #18212a; }
.nav-item small { color: #5d6a78; text-transform: uppercase; font-size: 9px; letter-spacing: .08em; }
.sidebar-footer { margin-top: auto; padding: 14px 8px 2px; display: flex; gap: 9px; align-items: flex-start; border-top: 1px solid #202a35; }
.sidebar-footer strong, .sidebar-footer small { display: block; }
.sidebar-footer strong { font-size: 12px; }
.sidebar-footer small { margin-top: 3px; color: #667485; font-size: 10px; word-break: break-all; }
.status-dot { width: 8px; height: 8px; margin-top: 4px; border-radius: 999px; flex: 0 0 auto; }
.status-dot.online { background: #79e2b5; box-shadow: 0 0 12px #79e2b580; }
.status-dot.offline { background: #ef7a7a; box-shadow: 0 0 12px #ef7a7a60; }

.content { width: min(1420px, 100%); padding: 42px 48px 80px; }
.page-header { display: flex; align-items: flex-start; justify-content: space-between; gap: 24px; margin-bottom: 28px; }
.eyebrow { margin: 0 0 8px; color: #75d9b0; font-size: 10px; font-weight: 700; letter-spacing: .16em; }
h1 { margin: 0; font-size: clamp(30px, 4vw, 44px); letter-spacing: -.04em; }
h2 { margin: 2px 0 0; font-size: 20px; letter-spacing: -.02em; }
h3 { margin: 10px 0 6px; }
.subtitle { margin: 8px 0 0; color: #8291a2; max-width: 680px; }
button { border: 1px solid #33404d; color: #dce5ed; background: #151c24; border-radius: 8px; padding: 9px 13px; cursor: pointer; }
button:hover:not(:disabled) { border-color: #526376; background: #1a232d; }
button:disabled { opacity: .45; cursor: default; }
.refresh { white-space: nowrap; }

.notice { display: flex; justify-content: space-between; align-items: center; gap: 20px; padding: 16px 18px; margin-bottom: 22px; border: 1px solid; border-radius: 10px; }
.notice strong { display: block; margin-bottom: 3px; }
.notice p { margin: 0; color: #93a0af; font-size: 13px; }
.notice.success { border-color: #244e3d; background: #0f201a; }
.notice.danger { border-color: #5b3232; background: #211313; }
code { color: #a9bac9; font-family: "SFMono-Regular", Consolas, monospace; font-size: 12px; }

.stats { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 14px; margin-bottom: 14px; }
.stat-card, .panel { border: 1px solid #202b36; border-radius: 12px; background: #10161dcc; box-shadow: 0 18px 50px #0002; }
.stat-card { padding: 20px; }
.stat-card > span { display: block; color: #7e8c9c; font-size: 12px; }
.stat-card > strong { display: block; margin: 8px 0 4px; font-size: 30px; letter-spacing: -.04em; }
.stat-card .text-value { font-size: 20px; padding: 7px 0; }
.stat-card small { color: #5e6d7c; }

.grid { display: grid; grid-template-columns: minmax(0, 2fr) minmax(300px, 1fr); gap: 14px; }
.panel { padding: 20px; }
.panel-heading { display: flex; justify-content: space-between; align-items: center; }
.empty-state { min-height: 310px; display: grid; place-items: center; align-content: center; text-align: center; color: #8391a0; padding: 30px; }
.empty-state p { max-width: 520px; margin: 0; line-height: 1.55; }
.empty-icon { width: 44px; height: 44px; display: grid; place-items: center; border: 1px solid #2a3845; border-radius: 10px; color: #78ddb4; font-size: 24px; }
.side-panel dl { margin: 22px 0 0; }
.side-panel dl > div { display: grid; gap: 5px; padding: 14px 0; border-top: 1px solid #202a34; }
dt { color: #697888; font-size: 11px; text-transform: uppercase; letter-spacing: .08em; }
dd { margin: 0; }

@media (max-width: 950px) {
  .shell { grid-template-columns: 76px 1fr; }
  .brand > div:last-child, .nav-item span, .nav-item small, .sidebar-footer div { display: none; }
  .brand { justify-content: center; padding-inline: 0; }
  .nav-item { min-height: 42px; justify-content: center; }
  .nav-item::before { content: '•'; font-size: 20px; }
  .sidebar-footer { justify-content: center; }
  .content { padding: 30px 24px 60px; }
  .stats { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .grid { grid-template-columns: 1fr; }
}

@media (max-width: 620px) {
  .shell { display: block; }
  .sidebar { position: static; width: 100%; height: auto; flex-direction: row; align-items: center; border-right: 0; border-bottom: 1px solid #202a35; }
  .brand { padding: 0; }
  nav { margin-left: auto; display: flex; }
  .nav-item:not(.active), .sidebar-footer { display: none; }
  .content { padding: 24px 16px 50px; }
  .page-header, .notice { align-items: flex-start; flex-direction: column; }
  .stats { grid-template-columns: 1fr; }
}
</style>
