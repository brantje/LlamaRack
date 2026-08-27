export type User = { id: number; username: string; enabled: boolean }
export type Model = {
  id: string
  name: string
  gguf_path: string
  total_bytes: number
  quantization?: string
  context_length: number
  // Transitional optional fields for older callers; the Models UI does not use them.
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
  model_id: string
  name: string
  enabled: boolean
  autoload_enabled: boolean
  always_on: boolean
  priority: 'low' | 'normal' | 'high' | string
  eviction_enabled: boolean
  idle_unload_seconds: number
  gpu_mode: 'auto' | 'manual' | string
  gpu_devices?: string[]
  tensor_split?: string
}
export type Runtime = { instance_id: string; model_id: string; state: string; pid?: number; port?: number; last_error?: string }
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
}
export type APIKey = { id: string; name: string; prefix: string; enabled: boolean; created_at: number; last_used_at?: number }
export type Profile = {
  path: string
  version?: string
  fingerprint: string
  options: Array<{ key: string; value_hint?: string; description?: string; kind?: string; choices?: string[] }>
}

type RuntimeEvent = { type: string; runtime?: Runtime; runtimes?: Runtime[]; telemetry?: RuntimeTelemetry[] }

let runtimeSocket: WebSocket | null = null
let runtimeReconnectTimer: ReturnType<typeof setTimeout> | undefined
const activeRuntimeStates = new Set(['STARTING', 'LOADING', 'READY', 'STOPPING'])

export function useManager() {
  const { request, apiBase } = useManagerApi()
  const user = useState<User | null>('manager-user', () => null)
  const initialized = useState('manager-initialized', () => false)
  const bootstrapRequired = useState('manager-bootstrap', () => false)
  const models = useState<Model[]>('manager-models', () => [])
  const instances = useState<Instance[]>('manager-instances', () => [])
  const runtimes = useState<Record<string, Runtime[]>>('manager-runtimes', () => ({}))
  const runtimeTelemetry = useState<Record<string, RuntimeTelemetry>>('manager-runtime-telemetry', () => ({}))
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

  function disconnectRuntimeEvents() {
    if (runtimeReconnectTimer) {
      clearTimeout(runtimeReconnectTimer)
      runtimeReconnectTimer = undefined
    }
    runtimeEventsConnected.value = false
    runtimeTelemetry.value = {}
    const socket = runtimeSocket
    runtimeSocket = null
    socket?.close()
  }

  function connectRuntimeEvents() {
    if (!import.meta.client || !user.value || runtimeSocket || typeof WebSocket === 'undefined') return
    if (runtimeReconnectTimer) {
      clearTimeout(runtimeReconnectTimer)
      runtimeReconnectTimer = undefined
    }
    let socket: WebSocket
    try {
      socket = new WebSocket(`${apiBase.value.replace(/^http/, 'ws')}/api/v1/ws`)
    } catch {
      return
    }
    runtimeSocket = socket
    socket.onopen = () => {
      if (runtimeSocket === socket) runtimeEventsConnected.value = true
    }
    socket.onmessage = (event) => {
      let message: RuntimeEvent
      try {
        message = JSON.parse(String(event.data)) as RuntimeEvent
      } catch {
        return
      }
      if (message.type === 'runtime_snapshot' && Array.isArray(message.runtimes)) applyRuntimeSnapshot(message.runtimes)
      else if (message.type === 'runtime' && message.runtime) applyRuntime(message.runtime)
      else if (message.type === 'runtime_telemetry' && Array.isArray(message.telemetry)) applyRuntimeTelemetry(message.telemetry)
    }
    socket.onclose = () => {
      if (runtimeSocket !== socket) return
      runtimeSocket = null
      runtimeEventsConnected.value = false
      runtimeTelemetry.value = {}
      if (!user.value) return
      runtimeReconnectTimer = setTimeout(() => {
        runtimeReconnectTimer = undefined
        connectRuntimeEvents()
      }, 1000)
    }
  }

  async function initialize() {
    backendError.value = ''
    try {
      const bootstrap = await request<{ required: boolean }>('/api/v1/auth/bootstrap')
      bootstrapRequired.value = bootstrap.required
      if (!bootstrap.required) {
        try {
          user.value = await request<User>('/api/v1/me')
          await refresh()
          connectRuntimeEvents()
        } catch {
          disconnectRuntimeEvents()
          user.value = null
        }
      } else {
        disconnectRuntimeEvents()
      }
    } catch (error: any) {
      disconnectRuntimeEvents()
      backendError.value = error?.message || 'Backend unavailable'
    } finally {
      initialized.value = true
    }
  }

  async function authenticate(username: string, password: string) {
    if (bootstrapRequired.value) {
      await request('/api/v1/auth/bootstrap', { method: 'POST', body: { username, password } })
      bootstrapRequired.value = false
    }
    user.value = await request<User>('/api/v1/auth/login', { method: 'POST', body: { username, password } })
    await refresh()
    connectRuntimeEvents()
  }

  async function logout() {
    await request('/api/v1/auth/logout', { method: 'POST' })
    disconnectRuntimeEvents()
    user.value = null
    models.value = []
    instances.value = []
    runtimes.value = {}
    runtimeTelemetry.value = {}
  }

  async function refresh() {
    if (!user.value) return
    const [modelItems, instanceItems] = await Promise.all([
      request<Model[]>('/api/v1/models'),
      request<Instance[]>('/api/v1/instances')
    ])
    models.value = modelItems || []
    instances.value = instanceItems || []
    if (runtimeEventsConnected.value) {
      runtimes.value = Object.fromEntries(models.value.map(model => [model.id, runtimes.value[model.id] || []]))
    } else {
      const runtimeItems = await Promise.all(instances.value.map(async instance => {
        try {
          return await request<Runtime>(`/api/v1/instances/${encodeURIComponent(instance.id)}/runtime`)
        } catch {
          return { instance_id: instance.id, model_id: instance.model_id, state: 'UNLOADED' } satisfies Runtime
        }
      }))
      applyRuntimeSnapshot(runtimeItems)
    }
    try {
      const result = await request<{ available: boolean; profile: Profile }>('/api/v1/llamacpp/profile')
      profile.value = result.profile
    } catch {
      profile.value = null
    }
  }

  function modelState(model: Model) {
    const items = runtimes.value[model.id] || []
    return items.find(x => x.state === 'READY')?.state
      || items.find(x => ['STARTING', 'LOADING'].includes(x.state))?.state
      || items.find(x => x.state === 'STOPPING')?.state
      || items.find(x => x.state === 'FAILED')?.state
      || 'UNLOADED'
  }

  function runtimeForInstance(instance: Instance) {
    return (runtimes.value[instance.model_id] || []).find(item => item.instance_id === instance.id)
      || { instance_id: instance.id, model_id: instance.model_id, state: 'UNLOADED' } as Runtime
  }

  function telemetryForInstance(instance: Instance) {
    return runtimeTelemetry.value[instance.id]
  }

  function instanceState(instance: Instance) {
    return runtimeForInstance(instance).state
  }

  return {
    apiBase,
    user,
    initialized,
    bootstrapRequired,
    models,
    instances,
    runtimes,
    runtimeTelemetry,
    profile,
    backendError,
    runtimeEventsConnected,
    initialize,
    authenticate,
    logout,
    refresh,
    modelState,
    runtimeForInstance,
    telemetryForInstance,
    instanceState,
    connectRuntimeEvents,
    disconnectRuntimeEvents,
    request
  }
}
