<script setup lang="ts">
const manager = useManager()
const { models, runtimes, profile, canOperate } = manager
const readyWorkers = computed(() => Object.values(runtimes.value).flat().filter(x => x.state === 'READY').length)
const failedWorkers = computed(() => Object.values(runtimes.value).flat().filter(x => x.state === 'FAILED').length)
</script>

<template>
  <div>
    <header class="page-header"><div><p class="eyebrow">LOCAL INFERENCE</p><h1>Overview</h1><p class="muted">Lifecycle, routing and llama.cpp runtime status.</p></div><button class="ghost" @click="manager.refresh">Refresh</button></header>
    <section class="stats-grid"><article class="stat-card"><span>Configured models</span><strong>{{ models.length }}</strong><small>available through manager</small></article><article class="stat-card"><span>Ready workers</span><strong>{{ readyWorkers }}</strong><small>serving inference</small></article><article class="stat-card"><span>Failed workers</span><strong>{{ failedWorkers }}</strong><small>need attention</small></article><article class="stat-card"><span>llama.cpp</span><strong class="compact-value">{{ profile?.version || 'Unavailable' }}</strong><small>{{ profile?.options.length || 0 }} discovered CLI options</small></article></section>
    <section class="panel"><div class="panel-header"><div><p class="eyebrow">FLEET</p><h2>Model activity</h2></div><NuxtLink v-if="canOperate" to="/models" class="button primary small">Manage models</NuxtLink></div><div v-if="!models.length" class="empty-state"><strong>No models configured</strong><p>Register a GGUF artifact and create a model to start serving requests.</p></div><div v-else class="table-wrap"><table><thead><tr><th>Model</th><th>Status</th><th>Loading</th><th>Priority</th><th>Artifact</th></tr></thead><tbody><tr v-for="model in models" :key="model.id"><td><strong>{{ model.model_id }}</strong><small>{{ model.display_name || '—' }}</small></td><td><span class="status" :data-state="manager.modelState(model)">{{ manager.modelState(model) }}</span></td><td>{{ model.always_on ? 'Always on' : model.autoload_enabled ? 'Autoload' : 'Manual' }}</td><td>{{ model.priority }}</td><td class="mono">{{ model.artifact_path }}</td></tr></tbody></table></div></section>
  </div>
</template>
