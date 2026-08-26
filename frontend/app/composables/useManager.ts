export type Role = 'admin' | 'operator' | 'readonly'
export type User = { id: number; username: string; role: Role; enabled: boolean }
export type Model = { id: string; model_id: string; name: string; gguf_path: string; total_bytes: number; quantization?: string; enabled: boolean; autoload_enabled: boolean; always_on: boolean; priority: string; eviction_enabled: boolean; idle_unload_seconds: number; routing_policy: string }
export type Runtime = { instance_id: string; model_id: string; state: string; pid?: number; port?: number; last_error?: string }
export type APIKey = { id: string; name: string; prefix: string; enabled: boolean; created_at: number; last_used_at?: number }
export type Profile = { path: string; version?: string; fingerprint: string; options: Array<{ key: string; value_hint?: string; description?: string }> }

export function useManager() {
  const { request, apiBase } = useManagerApi()
  const user = useState<User | null>('manager-user', () => null)
  const initialized = useState('manager-initialized', () => false)
  const bootstrapRequired = useState('manager-bootstrap', () => false)
  const models = useState<Model[]>('manager-models', () => [])
  const runtimes = useState<Record<string, Runtime[]>>('manager-runtimes', () => ({}))
  const profile = useState<Profile | null>('manager-profile', () => null)
  const backendError = useState('manager-backend-error', () => '')

  const canOperate = computed(() => user.value?.role === 'admin' || user.value?.role === 'operator')
  const isAdmin = computed(() => user.value?.role === 'admin')

  async function initialize() {
    backendError.value = ''
    try {
      const bootstrap = await request<{ required: boolean }>('/api/v1/auth/bootstrap')
      bootstrapRequired.value = bootstrap.required
      if (!bootstrap.required) {
        try {
          user.value = await request<User>('/api/v1/me')
          await refresh()
        } catch {
          user.value = null
        }
      }
    } catch (error: any) {
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
  }

  async function logout() {
    await request('/api/v1/auth/logout', { method: 'POST' })
    user.value = null
    models.value = []
    runtimes.value = {}
  }

  async function refresh() {
    if (!user.value) return
    const modelItems = await request<Model[]>('/api/v1/models')
    models.value = modelItems || []
    const runtimeEntries = await Promise.all(models.value.map(async model => {
      try {
        return [model.id, await request<Runtime[]>(`/api/v1/models/${model.id}/runtime`)] as const
      } catch {
        return [model.id, []] as const
      }
    }))
    runtimes.value = Object.fromEntries(runtimeEntries)
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
      || items.find(x => x.state === 'FAILED')?.state
      || 'UNLOADED'
  }

  return { apiBase, user, initialized, bootstrapRequired, models, runtimes, profile, backendError, canOperate, isAdmin, initialize, authenticate, logout, refresh, modelState, request }
}
