export type User = { id: number; username: string; enabled: boolean }
export type PublicOIDCProvider = { id: string; name: string }
export type LoginResult = { access_token: string; token_type: string; expires_at: number; user: User; remember?: boolean }
export type AuthProviderInfo = { local_login_enabled: boolean; providers: PublicOIDCProvider[] }
export type Model = {
  id: string
  slug: string
  name: string
  gguf_path: string
  total_bytes: number
  quantization?: string
  context_length: number
  model_id?: string
  enabled?: boolean
  autoload_enabled?: boolean
  always_on?: boolean
  priority?: string
  eviction_enabled?: boolean
  idle_unload_seconds?: number
  routing_policy?: string
}
export type Instance = {
  id: string
  slug: string
  model_id: string
  name: string
  enabled: boolean
  autoload_enabled: boolean
  always_on: boolean
  priority: 'low' | 'normal' | 'high' | string
  eviction_enabled: boolean
  idle_unload_seconds: number
  max_pending_requests?: number
  gpu_mode: 'auto' | 'manual' | string
  gpu_devices?: string[]
  tensor_split?: string
  request_log_mode?: 'metadata' | 'full' | string
}
export type Runtime = {
  instance_id: string
  model_id: string
  state: string
  pid?: number
  port?: number
  last_error?: string
  consecutive_start_failures?: number
  retry_after?: string
}
export type RuntimeGPUUsage = { device_id: string; vram_used_bytes?: number; utilization_pct?: number }
export type RuntimeTelemetry = {
  instance_id: string
  pid: number
  gpu_devices: string[]
  gpus: RuntimeGPUUsage[]
  vram_used_bytes?: number
  gpu_utilization_pct?: number
  cpu_percent?: number
  memory_used_bytes?: number
  collected_at: string
  llama_metrics?: Record<string, unknown>
}
export type HardwareGPU = { id: string; backend: string; index: number; uuid?: string; name: string; total_bytes: number; used_bytes: number; free_bytes: number; utilization_pct: number }
export type HardwareProcess = { pid: number; device_id: string; used_bytes: number; process_name?: string }
export type HardwareSnapshot = { ram_total_bytes: number; ram_available_bytes: number; gpus: HardwareGPU[]; processes: HardwareProcess[]; collected_at: string }
export type Percentiles = { p50?: number; p95?: number; p99?: number }
export type GatewaySummary = {
  since: number
  requests: number
  successes: number
  errors: number
  active: number
  queued: number
  active_api_keys: number
  prompt_tokens: number
  generated_tokens: number
  total_tokens: number
  latency_ms: Percentiles
  ttft_ms: Percentiles
}
export type ObservabilityRequest = {
  id: number
  started_at: number
  finished_at: number
  instance_id: string
  model_slug?: string
  endpoint: string
  api_key?: { id: string; name: string; prefix: string }
  streaming: boolean
  status_code: number
  result: string
  duration_ms: number
  ttft_ms?: number
  prompt_tokens: number
  generated_tokens: number
  total_tokens: number
  tokens_per_second?: number
  queue_duration_ms: number
  load_duration_ms: number
  autoloaded: boolean
  error?: string
  request_body?: string
  response_body?: string
}
export type ObservabilityLive = { collected_at: string; hardware: HardwareSnapshot; telemetry: RuntimeTelemetry[]; gateway: GatewaySummary; requests: ObservabilityRequest[] }
export type APIKeyType = 'inference' | 'management' | 'full'
export type APIKeyOwnerKind = 'user' | 'service_account'
export type APIKey = {
  id: string
  name: string
  prefix: string
  enabled: boolean
  key_type: APIKeyType
  owner_kind: APIKeyOwnerKind
  owner_id: string | number
  owner_name: string
  owner_enabled: boolean
  status?: 'enabled' | 'disabled' | 'owner_disabled' | 'expired' | string
  instance_ids?: string[]
  missing_instance_ids?: string[]
  expires_on?: string | null
  created_at: number
  last_used_at?: number
  created_by_user_id?: number
  managed?: boolean
}
export type ServiceAccount = {
  id: string
  name: string
  enabled: boolean
  created_at: number
  created_by_user_id?: number
  keys?: APIKey[]
}
export type Profile = { path: string; version?: string; fingerprint: string; options: Array<{ key: string; value_hint?: string; description?: string; kind?: string; choices?: string[] }> }

type RuntimeEvent = {
  type: string
  runtime?: Runtime
  runtimes?: Runtime[]
  telemetry?: RuntimeTelemetry[]
  collected_at?: string
  hardware?: HardwareSnapshot
  gateway?: GatewaySummary
  requests?: ObservabilityRequest[]
}

let runtimeSocket: WebSocket | null = null
let runtimeReconnectTimer: ReturnType<typeof setTimeout> | undefined
let runtimeConnecting = false
const activeRuntimeStates = new Set(['STARTING', 'LOADING', 'READY', 'DRAINING', 'STOPPING'])

export function useManager() {
  const { request, apiBase } = useManagerApi()
  const user = useState<User | null>('manager-user', () => null)
  const initialized = useState('manager-initialized', () => false)
  const bootstrapRequired = useState('manager-bootstrap', () => false)
  const localLoginEnabled = useState('manager-local-login-enabled', () => true)
  const authProviders = useState<PublicOIDCProvider[]>('manager-auth-providers', () => [])
  const models = useState<Model[]>('manager-models', () => [])
  const instances = useState<Instance[]>('manager-instances', () => [])
  const runtimes = useState<Record<string, Runtime[]>>('manager-runtimes', () => ({}))
  const runtimeTelemetry = useState<Record<string, RuntimeTelemetry>>('manager-runtime-telemetry', () => ({}))
  const observabilityLive = useState<ObservabilityLive | null>('manager-observability-live', () => null)
  const profile = useState<Profile | null>('manager-profile', () => null)
  const backendError = useState('manager-backend-error', () => '')
  const runtimeEventsConnected = useState('manager-runtime-events-connected', () => false)

  function clearRuntimeTelemetry(instanceID: string) {
    if (!runtimeTelemetry.value[instanceID]) return
    const next = { ...runtimeTelemetry.value }
    delete next[instanceID]
    runtimeTelemetry.value = next
  }

  function applyRuntime(runtime: Runtime) {
    if (!runtime.model_id || !runtime.instance_id) return
    const items = [...(runtimes.value[runtime.model_id] || [])]
    const index = items.findIndex(item => item.instance_id === runtime.instance_id)
    if (index === -1) items.push(runtime)
    else items[index] = runtime
    runtimes.value = { ...runtimes.value, [runtime.model_id]: items }
    if (!activeRuntimeStates.has(runtime.state) || !runtime.pid) clearRuntimeTelemetry(runtime.instance_id)
  }

  function applyRuntimeSnapshot(snapshot: Runtime[]) {
    const grouped: Record<string, Runtime[]> = {}
    for (const runtime of snapshot) {
      if (!runtime.model_id || !runtime.instance_id) continue
      ;(grouped[runtime.model_id] ||= []).push(runtime)
    }
    runtimes.value = Object.fromEntries(models.value.map(model => [model.id, grouped[model.id] || []]))
    runtimeTelemetry.value = {}
  }

  function applyRuntimeTelemetry(samples: RuntimeTelemetry[]) {
    const next = { ...runtimeTelemetry.value }
    for (const sample of samples) {
      if (!sample?.instance_id || !Number.isFinite(sample.pid) || sample.pid <= 0) continue
      const instance = instances.value.find(item => item.id === sample.instance_id)
      if (!instance) continue
      const runtime = (runtimes.value[instance.model_id] || []).find(item => item.instance_id === instance.id)
      if (!runtime || !activeRuntimeStates.has(runtime.state) || runtime.pid !== sample.pid) continue
      next[sample.instance_id] = sample
    }
    runtimeTelemetry.value = next
  }

  function applyObservability(message: RuntimeEvent) {
    if (!message.hardware || !message.collected_at) return
    const telemetry = Array.isArray(message.telemetry) ? message.telemetry : []
    const gateway = message.gateway || {
      since: Date.now() - 15 * 60 * 1000,
      requests: 0,
      successes: 0,
      errors: 0,
      active: 0,
      queued: 0,
      active_api_keys: 0,
      prompt_tokens: 0,
      generated_tokens: 0,
      total_tokens: 0,
      latency_ms: {},
      ttft_ms: {}
    }
    observabilityLive.value = { collected_at: message.collected_at, hardware: message.hardware, telemetry, gateway, requests: Array.isArray(message.requests) ? message.requests : [] }
    if (telemetry.length) applyRuntimeTelemetry(telemetry)
  }

  function disconnectRuntimeEvents() {
    if (runtimeReconnectTimer) {
      clearTimeout(runtimeReconnectTimer)
      runtimeReconnectTimer = undefined
    }
    runtimeConnecting = false
    runtimeEventsConnected.value = false
    runtimeTelemetry.value = {}
    observabilityLive.value = null
    const socket = runtimeSocket
    runtimeSocket = null
    socket?.close()
  }

  async function connectRuntimeEvents() {
    if (!import.meta.client || !user.value || runtimeSocket || runtimeConnecting || typeof WebSocket === 'undefined') return
    if (runtimeReconnectTimer) {
      clearTimeout(runtimeReconnectTimer)
      runtimeReconnectTimer = undefined
    }
    runtimeConnecting = true
    let ticket = ''
    try {
      const result = await request<{ ticket: string }>('/api/v1/auth/ws-ticket', { method: 'POST' })
      ticket = result.ticket
    } catch {
      runtimeConnecting = false
      return
    }
    if (!user.value || !ticket) {
      runtimeConnecting = false
      return
    }
    let socket: WebSocket
    try {
      socket = new WebSocket(`${apiBase.value.replace(/^http/, 'ws')}/api/v1/ws?ticket=${encodeURIComponent(ticket)}`)
    } catch {
      runtimeConnecting = false
      return
    }
    runtimeConnecting = false
    runtimeSocket = socket
    socket.onopen = () => { if (runtimeSocket === socket) runtimeEventsConnected.value = true }
    socket.onmessage = (event) => {
      let message: RuntimeEvent
      try { message = JSON.parse(String(event.data)) as RuntimeEvent } catch { return }
      if (message.type === 'runtime_snapshot' && Array.isArray(message.runtimes)) applyRuntimeSnapshot(message.runtimes)
      else if (message.type === 'runtime' && message.runtime) applyRuntime(message.runtime)
      else if (message.type === 'runtime_telemetry' && Array.isArray(message.telemetry)) applyRuntimeTelemetry(message.telemetry)
      else if (message.type === 'observability') applyObservability(message)
    }
    socket.onclose = () => {
      if (runtimeSocket !== socket) return
      runtimeSocket = null
      runtimeEventsConnected.value = false
      runtimeTelemetry.value = {}
      observabilityLive.value = null
      if (!user.value) return
      runtimeReconnectTimer = setTimeout(() => { runtimeReconnectTimer = undefined; void connectRuntimeEvents() }, 1000)
    }
  }

  async function refreshAuthProviders() {
    const response = await request<AuthProviderInfo>('/api/v1/auth/providers')
    localLoginEnabled.value = response.local_login_enabled
    authProviders.value = response.providers || []
  }

  async function initialize() {
    backendError.value = ''
    try {
      const [bootstrap] = await Promise.all([
        request<{ required: boolean }>('/api/v1/auth/bootstrap'),
        refreshAuthProviders()
      ])
      bootstrapRequired.value = bootstrap.required
      if (!bootstrap.required && readManagementToken()) {
        try {
          user.value = await request<User>('/api/v1/me')
          await refresh()
          void connectRuntimeEvents()
        } catch {
          clearManagementToken()
          disconnectRuntimeEvents()
          user.value = null
        }
      } else {
        disconnectRuntimeEvents()
        user.value = null
      }
    } catch (error: any) {
      disconnectRuntimeEvents()
      backendError.value = error?.message || 'Backend unavailable'
    } finally {
      initialized.value = true
    }
  }

  async function authenticate(username: string, password: string, remember = false) {
    if (bootstrapRequired.value) {
      await request('/api/v1/auth/bootstrap', { method: 'POST', body: { username, password } })
      bootstrapRequired.value = false
    }
    const result = await request<LoginResult>('/api/v1/auth/login', { method: 'POST', body: { username, password } })
    storeManagementToken(result.access_token, remember)
    user.value = result.user
    await refresh()
    void connectRuntimeEvents()
  }

  function beginOIDC(providerID: string, remember = false) {
    if (!import.meta.client) return
    const url = `${apiBase.value}/api/v1/auth/oidc/${encodeURIComponent(providerID)}/start?remember=${remember ? 'true' : 'false'}`
    window.location.assign(url)
  }

  async function exchangeOIDC(code: string) {
    const result = await request<LoginResult>('/api/v1/auth/oidc/exchange', { method: 'POST', body: { code } })
    storeManagementToken(result.access_token, Boolean(result.remember))
    user.value = result.user
    await refresh()
    void connectRuntimeEvents()
    return result
  }

  async function logout() {
    try {
      if (readManagementToken()) await request('/api/v1/auth/logout', { method: 'POST' })
    } finally {
      clearManagementToken()
      disconnectRuntimeEvents()
      user.value = null
      models.value = []
      instances.value = []
      runtimes.value = {}
      runtimeTelemetry.value = {}
      observabilityLive.value = null
    }
  }

  async function refresh() {
    if (!user.value) return
    const [modelItems, instanceItems] = await Promise.all([request<Model[]>('/api/v1/models'), request<Instance[]>('/api/v1/instances')])
    models.value = (modelItems || []).map(model => ({ ...model, slug: model.slug || model.id }))
    instances.value = (instanceItems || []).map(instance => ({ ...instance, slug: instance.slug || instance.id }))
    if (runtimeEventsConnected.value) {
      runtimes.value = Object.fromEntries(models.value.map(model => [model.id, runtimes.value[model.id] || []]))
    } else {
      const runtimeItems = await Promise.all(instances.value.map(async instance => {
        try { return await request<Runtime>(`/api/v1/instances/${encodeURIComponent(instance.slug)}/runtime`) }
        catch { return { instance_id: instance.id, model_id: instance.model_id, state: 'UNLOADED' } satisfies Runtime }
      }))
      applyRuntimeSnapshot(runtimeItems)
    }
    try {
      const result = await request<{ available: boolean; profile: Profile }>('/api/v1/llamacpp/profile')
      profile.value = result.profile
    } catch { profile.value = null }
  }

  function modelState(model: Model) {
    const items = runtimes.value[model.id] || []
    return items.find(x => x.state === 'READY')?.state || items.find(x => ['STARTING', 'LOADING'].includes(x.state))?.state || items.find(x => x.state === 'STOPPING')?.state || items.find(x => x.state === 'DRAINING')?.state || items.find(x => x.state === 'FAILED')?.state || 'UNLOADED'
  }
  function runtimeForInstance(instance: Instance) { return (runtimes.value[instance.model_id] || []).find(item => item.instance_id === instance.id) || { instance_id: instance.id, model_id: instance.model_id, state: 'UNLOADED' } as Runtime }
  function telemetryForInstance(instance: Instance) { return runtimeTelemetry.value[instance.id] }
  function instanceState(instance: Instance) { return runtimeForInstance(instance).state }

  return {
    apiBase, user, initialized, bootstrapRequired, localLoginEnabled, authProviders,
    models, instances, runtimes, runtimeTelemetry, observabilityLive, profile, backendError, runtimeEventsConnected,
    initialize, authenticate, beginOIDC, exchangeOIDC, logout, refresh, refreshAuthProviders,
    modelState, runtimeForInstance, telemetryForInstance, instanceState, connectRuntimeEvents, disconnectRuntimeEvents, request
  }
}