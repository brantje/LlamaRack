export type User = { id: number; username: string; enabled: boolean }
export type Model = { id: string; model_id: string; name: string; gguf_path: string; total_bytes: number; quantization?: string; enabled: boolean; autoload_enabled: boolean; always_on: boolean; priority: string; eviction_enabled: boolean; idle_unload_seconds: number; routing_policy: string }
export type Runtime = { instance_id: string; model_id: string; state: string; pid?: number; port?: number; last_error?: string }
export type APIKey = { id: string; name: string; prefix: string; enabled: boolean; created_at: number; last_used_at?: number }
export type Profile = { path: string; version?: string; fingerprint: string; options: Array<{ key: string; value_hint?: string; description?: string }> }

type RuntimeEvent = { type: string; runtime?: Runtime }

let runtimeSocket: WebSocket | null = null
let runtimeReconnectTimer: ReturnType<typeof setTimeout> | undefined

export function useManager() {
  const { request, apiBase } = useManagerApi()
  const user = useState<User | null>('manager-user', () => null)
  const initialized = useState('manager-initialized', () => false)
  const bootstrapRequired = useState('manager-bootstrap', () => false)
  const models = useState<Model[]>('manager-models', () => [])
  const runtimes = useState<Record<string, Runtime[]>>('manager-runtimes', () => ({}))
  const profile = useState<Profile | null>('manager-profile', () => null)
  const backendError = useState('manager-backend-error', () => '')
  const runtimeEventsConnected = useState('manager-runtime-events-connected', () => false)

  function applyRuntime(runtime: Runtime) {
    if (!runtime.model_id || !runtime.instance_id) return
    const items = [...(runtimes.value[runtime.model_id] || [])]
    const index = items.findIndex(item => item.instance_id === runtime.instance_id)
    if (index === -1) items.push(runtime)
    else items[index] = runtime
    runtimes.value = { ...runtimes.value, [runtime.model_id]: items }
  }

  function disconnectRuntimeEvents() {
    if (runtimeReconnectTimer) {
      clearTimeout(runtimeReconnectTimer)
      runtimeReconnectTimer = undefined
    }
    runtimeEventsConnected.value = false
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
      if (message.type === 'runtime' && message.runtime) applyRuntime(message.runtime)
    }
    socket.onclose = () => {
      if (runtimeSocket !== socket) return
      runtimeSocket = null
      runtimeEventsConnected.value = false
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
    runtimes.value = {}
  }

  async function refresh() {
    if (!user.value) return
    const modelItems = await request<Model[]>('/api/v1/models')
    models.value = modelItems || []
    if (runtimeEventsConnected.value) {
      runtimes.value = Object.fromEntries(models.value.map(model => [model.id, runtimes.value[model.id] || []]))
    } else {
      const runtimeEntries = await Promise.all(models.value.map(async model => {
        try {
          return [model.id, await request<Runtime[]>(`/api/v1/models/${model.id}/runtime`)] as const
        } catch {
          return [model.id, []] as const
        }
      }))
      runtimes.value = Object.fromEntries(runtimeEntries)
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

  return {
    apiBase,
    user,
    initialized,
    bootstrapRequired,
    models,
    runtimes,
    profile,
    backendError,
    runtimeEventsConnected,
    initialize,
    authenticate,
    logout,
    refresh,
    modelState,
    connectRuntimeEvents,
    disconnectRuntimeEvents,
    request
  }
}
