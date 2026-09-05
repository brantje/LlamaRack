const managementTokenKey = 'llamarack_management_token'

export function readManagementToken() {
  if (typeof window === 'undefined') return ''
  return window.sessionStorage.getItem(managementTokenKey) || window.localStorage.getItem(managementTokenKey) || ''
}

export function storeManagementToken(token: string, remember: boolean) {
  if (typeof window === 'undefined') return
  window.sessionStorage.removeItem(managementTokenKey)
  window.localStorage.removeItem(managementTokenKey)
  if (!token) return
  const storage = remember ? window.localStorage : window.sessionStorage
  storage.setItem(managementTokenKey, token)
}

export function clearManagementToken() {
  if (typeof window === 'undefined') return
  window.sessionStorage.removeItem(managementTokenKey)
  window.localStorage.removeItem(managementTokenKey)
}

export function useManagerApi(fetcher: typeof $fetch = $fetch) {
  const config = useRuntimeConfig()
  const requestURL = useRequestURL()
  const apiBase = computed(() => {
    const configured = String(config.public.apiBase || '').replace(/\/$/, '')
    if (configured) return configured
    return requestURL.origin
  })

  async function request<T = unknown>(path: string, options: any = {}): Promise<T> {
    const headers = new Headers(options.headers || {})
    const token = readManagementToken()
    if (token && !headers.has('Authorization')) headers.set('Authorization', `Bearer ${token}`)
    return await fetcher<T>(`${apiBase.value}${path}`, { ...options, headers })
  }

  return { apiBase, request }
}
