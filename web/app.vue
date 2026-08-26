<script setup lang="ts">
type Role = 'admin' | 'operator' | 'readonly'
type User = { id:number; username:string; role:Role }
type Model = { id:string; model_id:string; display_name?:string; artifact_id:string; artifact_path?:string; enabled:boolean; autoload_enabled:boolean; always_on:boolean; priority:string; routing_policy:string }
type Artifact = { id:string; display_name:string; local_path:string; quantization?:string; provider:string }
type Runtime = { instance_id:string; model_id:string; state:string; pid?:number; port?:number; last_error?:string }
type Profile = { path:string; version:string; fingerprint:string; options:Array<{key:string;type:string;description:string}> }

type View = 'dashboard'|'models'|'api'|'settings'
const view = ref<View>('dashboard')
const user = ref<User|null>(null)
const bootstrapRequired = ref(false)
const authReady = ref(false)
const error = ref('')
const busy = ref(false)
const models = ref<Model[]>([])
const artifacts = ref<Artifact[]>([])
const runtime = ref<Record<string, Runtime[]>>({})
const profile = ref<Profile|null>(null)
const apiKeys = ref<any[]>([])
const newSecret = ref('')

const credentials = reactive({ username:'', password:'' })
const artifactForm = reactive({ path:'', display_name:'' })
const modelForm = reactive({ model_id:'', display_name:'', artifact_id:'', always_on:false, autoload_enabled:true, priority:'normal', routing_policy:'least_active' })
const apiKeyName = ref('default')

const canOperate = computed(() => user.value?.role === 'admin' || user.value?.role === 'operator')
const isAdmin = computed(() => user.value?.role === 'admin')
const readyCount = computed(() => Object.values(runtime.value).flat().filter(x => x.state === 'READY').length)
const activeProblems = computed(() => Object.values(runtime.value).flat().filter(x => x.state === 'FAILED'))

async function api<T>(path:string, init:RequestInit = {}):Promise<T> {
  const response = await fetch(path, { credentials:'include', ...init, headers:{ 'Content-Type':'application/json', ...(init.headers || {}) } })
  if (!response.ok) {
    let message = `${response.status} ${response.statusText}`
    try { const body = await response.json(); message = body.error || body.message || message } catch {}
    throw new Error(message)
  }
  if (response.status === 204) return undefined as T
  return await response.json()
}

async function initialize() {
  error.value=''
  try {
    const state = await api<{required:boolean}>('/api/v1/auth/bootstrap')
    bootstrapRequired.value = state.required
    if (!state.required) {
      try { user.value = await api<User>('/api/v1/me'); await refreshAll() } catch { user.value = null }
    }
  } catch (e:any) { error.value=e.message }
  finally { authReady.value=true }
}

async function submitAuth() {
  busy.value=true; error.value=''
  try {
    if (bootstrapRequired.value) {
      await api<User>('/api/v1/auth/bootstrap',{method:'POST',body:JSON.stringify(credentials)})
      bootstrapRequired.value=false
    }
    user.value = await api<User>('/api/v1/auth/login',{method:'POST',body:JSON.stringify(credentials)})
    credentials.password=''
    await refreshAll()
  } catch(e:any){ error.value=e.message }
  finally{ busy.value=false }
}

async function logout(){ await api('/api/v1/auth/logout',{method:'POST'}); user.value=null; models.value=[]; runtime.value={} }

async function refreshAll(){ await Promise.all([loadModels(),loadArtifacts(),loadProfile(), isAdmin.value ? loadKeys() : Promise.resolve()]) }
async function loadModels(){
  models.value=await api<Model[]>('/api/v1/models')
  const entries = await Promise.all(models.value.map(async m => [m.id, await api<Runtime[]>(`/api/v1/models/${m.id}/runtime`)] as const))
  runtime.value=Object.fromEntries(entries)
}
async function loadArtifacts(){ artifacts.value=await api<Artifact[]>('/api/v1/artifacts') }
async function loadProfile(){ try { const result=await api<any>('/api/v1/llamacpp/profile'); profile.value=result.profile } catch { profile.value=null } }
async function loadKeys(){ apiKeys.value=await api<any[]>('/api/v1/api-keys') }

function aggregateState(model:Model){ const items=runtime.value[model.id]||[]; return items.find(x=>x.state==='READY')?.state || items.find(x=>['LOADING','STARTING'].includes(x.state))?.state || items.find(x=>x.state==='FAILED')?.state || 'UNLOADED' }

async function registerArtifact(){ busy.value=true;error.value='';try{const created=await api<Artifact>('/api/v1/artifacts/register',{method:'POST',body:JSON.stringify(artifactForm)});artifacts.value.unshift(created);modelForm.artifact_id=created.id;artifactForm.path='';artifactForm.display_name=''}catch(e:any){error.value=e.message}finally{busy.value=false} }
async function createModel(){ busy.value=true;error.value='';try{await api('/api/v1/models',{method:'POST',body:JSON.stringify(modelForm)});modelForm.model_id='';modelForm.display_name='';await loadModels()}catch(e:any){error.value=e.message}finally{busy.value=false} }
async function lifecycleAction(model:Model,action:'start'|'stop'){ busy.value=true;error.value='';try{await api(`/api/v1/models/${model.id}/${action}`,{method:'POST'});await loadModels()}catch(e:any){error.value=e.message}finally{busy.value=false} }
async function createKey(){busy.value=true;error.value='';try{const result=await api<any>('/api/v1/api-keys',{method:'POST',body:JSON.stringify({name:apiKeyName.value})});newSecret.value=result.secret;await loadKeys()}catch(e:any){error.value=e.message}finally{busy.value=false}}
async function revokeKey(id:string){await api(`/api/v1/api-keys/${id}/revoke`,{method:'POST'});await loadKeys()}

onMounted(initialize)
</script>

<template>
  <div v-if="!authReady" class="center"><div class="loader"/><p>Starting manager…</p></div>

  <main v-else-if="!user" class="auth-shell">
    <section class="auth-card">
      <div class="brand-mark">λ</div>
      <p class="eyebrow">LLAMA.CPP CONTROL PLANE</p>
      <h1>{{ bootstrapRequired ? 'Create administrator' : 'Welcome back' }}</h1>
      <p class="muted">{{ bootstrapRequired ? 'Set up the first local admin account.' : 'Sign in to manage local inference.' }}</p>
      <p v-if="error" class="error">{{ error }}</p>
      <form @submit.prevent="submitAuth">
        <label>Username<input v-model="credentials.username" autocomplete="username" required></label>
        <label>Password<input v-model="credentials.password" type="password" :autocomplete="bootstrapRequired ? 'new-password' : 'current-password'" minlength="10" required></label>
        <button class="primary" :disabled="busy">{{ busy ? 'Working…' : bootstrapRequired ? 'Create admin' : 'Sign in' }}</button>
      </form>
    </section>
  </main>

  <div v-else class="shell">
    <aside>
      <div class="logo"><span>λ</span><strong>llamacpp<br>manager</strong></div>
      <nav>
        <button :class="{active:view==='dashboard'}" @click="view='dashboard'">Overview</button>
        <button :class="{active:view==='models'}" @click="view='models'">Models</button>
        <button :class="{active:view==='api'}" @click="view='api'">API</button>
        <button :class="{active:view==='settings'}" @click="view='settings'">Settings</button>
      </nav>
      <div class="identity"><div><strong>{{ user.username }}</strong><small>{{ user.role }}</small></div><button @click="logout">Sign out</button></div>
    </aside>

    <main class="content">
      <header><div><p class="eyebrow">LOCAL INFERENCE</p><h1>{{ view === 'dashboard' ? 'Overview' : view[0].toUpperCase()+view.slice(1) }}</h1></div><button class="ghost" @click="refreshAll">Refresh</button></header>
      <p v-if="error" class="error">{{ error }}</p>

      <template v-if="view==='dashboard'">
        <section class="stats">
          <article><span>Configured</span><strong>{{ models.length }}</strong><small>models</small></article>
          <article><span>Ready</span><strong>{{ readyCount }}</strong><small>workers</small></article>
          <article><span>Problems</span><strong>{{ activeProblems.length }}</strong><small>failed workers</small></article>
          <article><span>llama.cpp</span><strong class="version">{{ profile?.version || 'Unavailable' }}</strong><small>{{ profile?.options.length || 0 }} detected options</small></article>
        </section>
        <section class="panel">
          <div class="panel-head"><div><p class="eyebrow">FLEET</p><h2>Model activity</h2></div><button v-if="canOperate" class="primary small" @click="view='models'">Manage models</button></div>
          <div v-if="!models.length" class="empty">No models configured yet. Register a GGUF artifact and create your first model.</div>
          <table v-else><thead><tr><th>Model</th><th>Status</th><th>Policy</th><th>Priority</th><th>Artifact</th></tr></thead><tbody><tr v-for="m in models" :key="m.id"><td><strong>{{m.model_id}}</strong><small>{{m.display_name}}</small></td><td><span class="status" :data-state="aggregateState(m)">{{aggregateState(m)}}</span></td><td>{{m.always_on?'Always on':m.autoload_enabled?'Autoload':'Manual'}}</td><td>{{m.priority}}</td><td class="mono">{{m.artifact_path}}</td></tr></tbody></table>
        </section>
      </template>

      <template v-else-if="view==='models'">
        <div class="grid two">
          <section class="panel">
            <div class="panel-head"><div><p class="eyebrow">MODEL REGISTRY</p><h2>Configured models</h2></div></div>
            <div v-if="!models.length" class="empty">Nothing configured.</div>
            <article v-for="m in models" :key="m.id" class="model-row">
              <div class="grow"><div class="model-title"><strong>{{m.model_id}}</strong><span class="status" :data-state="aggregateState(m)">{{aggregateState(m)}}</span></div><small>{{m.artifact_path}} · {{m.priority}} · {{m.routing_policy}}</small></div>
              <div v-if="canOperate" class="actions"><button class="ghost" @click="lifecycleAction(m,'start')">Start</button><button class="danger" @click="lifecycleAction(m,'stop')">Stop</button></div>
            </article>
          </section>

          <div class="stack" v-if="canOperate">
            <section class="panel"><p class="eyebrow">LOCAL ARTIFACT</p><h2>Register GGUF</h2><p class="muted">The file must already exist inside the configured models directory.</p><form @submit.prevent="registerArtifact"><label>Path<input v-model="artifactForm.path" placeholder="/models/model.gguf" required></label><label>Display name<input v-model="artifactForm.display_name" placeholder="Optional"></label><button class="primary" :disabled="busy">Register artifact</button></form></section>
            <section class="panel"><p class="eyebrow">NEW MODEL</p><h2>Create model</h2><form @submit.prevent="createModel"><label>Public model ID<input v-model="modelForm.model_id" placeholder="qwen-coder" required></label><label>Display name<input v-model="modelForm.display_name" placeholder="Optional"></label><label>Artifact<select v-model="modelForm.artifact_id" required><option value="" disabled>Select artifact</option><option v-for="a in artifacts" :key="a.id" :value="a.id">{{a.display_name}}</option></select></label><div class="inline"><label>Priority<select v-model="modelForm.priority"><option>low</option><option>normal</option><option>high</option></select></label><label>Routing<select v-model="modelForm.routing_policy"><option value="least_active">Least active</option><option value="round_robin">Round robin</option></select></label></div><label class="check"><input v-model="modelForm.autoload_enabled" type="checkbox"> Autoload on request</label><label class="check"><input v-model="modelForm.always_on" type="checkbox"> Always on</label><button class="primary" :disabled="busy">Create model</button></form></section>
          </div>
        </div>
      </template>

      <template v-else-if="view==='api'">
        <section class="panel"><p class="eyebrow">OPENAI COMPATIBILITY</p><h2>Unified endpoint</h2><div class="endpoint">{{ typeof window !== 'undefined' ? window.location.origin : '' }}/v1</div><p class="muted">Use a manager API key and your configured public model ID with OpenAI-compatible SDKs or LiteLLM.</p></section>
        <section v-if="isAdmin" class="panel"><div class="panel-head"><div><p class="eyebrow">CREDENTIALS</p><h2>API keys</h2></div><div class="create-key"><input v-model="apiKeyName" placeholder="Key name"><button class="primary small" @click="createKey">Create</button></div></div><div v-if="newSecret" class="secret"><strong>Copy this key now — it will not be shown again.</strong><code>{{newSecret}}</code><button class="ghost" @click="navigator.clipboard.writeText(newSecret)">Copy</button></div><table v-if="apiKeys.length"><thead><tr><th>Name</th><th>Prefix</th><th>Status</th><th></th></tr></thead><tbody><tr v-for="key in apiKeys" :key="key.id"><td>{{key.name}}</td><td class="mono">{{key.prefix}}…</td><td>{{key.enabled?'Enabled':'Revoked'}}</td><td><button v-if="key.enabled" class="danger" @click="revokeKey(key.id)">Revoke</button></td></tr></tbody></table></section>
      </template>

      <template v-else>
        <section class="panel"><p class="eyebrow">LLAMA.CPP BINARY</p><h2>Runtime capabilities</h2><div v-if="profile" class="settings-list"><div><span>Binary</span><code>{{profile.path}}</code></div><div><span>Version</span><code>{{profile.version || 'unknown'}}</code></div><div><span>Fingerprint</span><code>{{profile.fingerprint.slice(0,20)}}…</code></div><div><span>Detected CLI options</span><strong>{{profile.options.length}}</strong></div></div><div v-else class="error">llama-server could not be discovered. Set LCM_LLAMA_SERVER to a valid binary.</div></section>
      </template>
    </main>
  </div>
</template>

<style>
:root{font-family:Inter,ui-sans-serif,system-ui,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;color:#e9eef5;background:#0b0f14;font-synthesis:none}*{box-sizing:border-box}body{margin:0;background:radial-gradient(circle at 70% 0,#17212b 0,#0b0f14 38%);min-height:100vh}button,input,select{font:inherit}button{cursor:pointer}.center,.auth-shell{min-height:100vh;display:grid;place-items:center}.center{color:#8fa0b3}.loader{width:28px;height:28px;border:2px solid #33404d;border-top-color:#8df0c7;border-radius:50%;animation:spin .8s linear infinite}@keyframes spin{to{transform:rotate(360deg)}}.auth-card{width:min(430px,calc(100vw - 32px));padding:42px;border:1px solid #26313c;background:#10161dcc;border-radius:18px;box-shadow:0 30px 80px #0008}.brand-mark,.logo span{color:#8df0c7;font-size:34px;font-weight:800}.auth-card h1,.content h1,.panel h2{margin:.2rem 0}.eyebrow{font-size:11px;letter-spacing:.16em;color:#6e8297;font-weight:800;margin:0}.muted,small{color:#7f91a3}.auth-card form,.panel form{display:grid;gap:14px;margin-top:26px}label{display:grid;gap:7px;font-size:12px;color:#9eacba;font-weight:700}input,select{width:100%;background:#0b1016;color:#e8eef4;border:1px solid #2a3642;border-radius:8px;padding:11px 12px;outline:none}input:focus,select:focus{border-color:#6fbda2;box-shadow:0 0 0 3px #64d6ae14}.primary,.ghost,.danger{border-radius:8px;padding:10px 14px;font-weight:750;border:1px solid transparent}.primary{background:#8df0c7;color:#08120e}.primary:hover{background:#a5f7d6}.ghost{background:#151d25;color:#c7d1dc;border-color:#2b3742}.danger{background:#27161a;color:#ffabb6;border-color:#5b2830}.small{padding:7px 11px;font-size:12px}.error{padding:11px 13px;border:1px solid #713541;background:#321b20;color:#ffb4bd;border-radius:8px}.shell{display:grid;grid-template-columns:230px 1fr;min-height:100vh}aside{position:sticky;top:0;height:100vh;border-right:1px solid #202a34;background:#0c1117;padding:26px 18px;display:flex;flex-direction:column}.logo{display:flex;gap:11px;align-items:center;padding:4px 8px 28px}.logo strong{font-size:13px;line-height:1.05;letter-spacing:.02em}nav{display:grid;gap:4px}nav button{border:0;background:transparent;color:#8191a2;text-align:left;border-radius:8px;padding:10px 12px;font-weight:650}nav button:hover,nav button.active{color:#eef5fa;background:#172028}.identity{margin-top:auto;border-top:1px solid #202a34;padding:18px 8px 0;display:grid;gap:10px}.identity small,.model-row small,td small{display:block;margin-top:3px}.identity button{background:none;border:0;padding:0;text-align:left;color:#71869a;font-size:12px}.content{padding:36px clamp(24px,4vw,64px);max-width:1600px;width:100%;overflow:hidden}.content>header{display:flex;justify-content:space-between;align-items:center;margin-bottom:30px}.content h1{font-size:30px}.stats{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:14px;margin-bottom:18px}.stats article,.panel{background:#10171e;border:1px solid #202c37;border-radius:12px}.stats article{padding:20px;display:grid;gap:3px}.stats span{font-size:11px;color:#6f8295;text-transform:uppercase;letter-spacing:.08em}.stats strong{font-size:28px}.stats .version{font-size:14px;word-break:break-word;margin:6px 0}.panel{padding:22px;margin-bottom:18px}.panel-head{display:flex;align-items:center;justify-content:space-between;gap:16px;margin-bottom:18px}.empty{border:1px dashed #2a3946;border-radius:9px;padding:34px;color:#72879a;text-align:center}table{width:100%;border-collapse:collapse;font-size:13px}th{text-align:left;color:#677b8e;text-transform:uppercase;letter-spacing:.08em;font-size:10px;padding:10px;border-bottom:1px solid #26323d}td{padding:13px 10px;border-bottom:1px solid #1c2731;color:#b8c5d0}tbody tr:last-child td{border-bottom:0}.status{display:inline-flex;padding:4px 7px;border:1px solid #34424f;border-radius:5px;font-size:10px;font-weight:800;letter-spacing:.04em}.status[data-state="READY"]{color:#8df0c7;border-color:#235c49;background:#10261e}.status[data-state="FAILED"]{color:#ff9eaa;border-color:#60313a;background:#29171b}.status[data-state="LOADING"],.status[data-state="STARTING"]{color:#ffd58c;border-color:#675129;background:#2b2314}.grid.two{display:grid;grid-template-columns:minmax(0,1.55fr) minmax(320px,.8fr);gap:18px}.stack{display:grid;align-content:start}.model-row{display:flex;align-items:center;gap:14px;padding:15px 0;border-bottom:1px solid #202a34}.model-row:last-child{border-bottom:0}.grow{flex:1;min-width:0}.model-title{display:flex;align-items:center;gap:10px}.actions{display:flex;gap:7px}.inline{display:grid;grid-template-columns:1fr 1fr;gap:10px}.check{display:flex!important;grid-template-columns:auto 1fr;align-items:center}.check input{width:auto}.endpoint,.secret{background:#0a1016;border:1px solid #263644;border-radius:8px;padding:14px;font-family:ui-monospace,SFMono-Regular,Menlo,monospace;color:#9ce8ca;word-break:break-all}.create-key{display:flex;gap:8px}.secret{display:grid;gap:10px;margin-bottom:18px;color:#d6e4ee}.secret code{color:#8df0c7}.settings-list{display:grid}.settings-list>div{display:flex;justify-content:space-between;gap:20px;padding:13px 0;border-bottom:1px solid #202a34}.settings-list span{color:#8294a5}.mono,code{font-family:ui-monospace,SFMono-Regular,Menlo,monospace;font-size:12px}@media(max-width:1050px){.stats{grid-template-columns:1fr 1fr}.grid.two{grid-template-columns:1fr}}@media(max-width:720px){.shell{grid-template-columns:1fr}aside{position:static;height:auto;border-right:0;border-bottom:1px solid #202a34}.logo{padding-bottom:14px}nav{display:flex;overflow:auto}.identity{display:none}.content{padding:24px 16px}.stats{grid-template-columns:1fr 1fr}table{display:block;overflow:auto}.content>header{margin-bottom:20px}}
</style>
