const mutationMethods = new Set(['POST', 'PUT', 'PATCH', 'DELETE'])

function csrfTokenFromCookie() {
  if (!import.meta.client || typeof document === 'undefined') return ''
  for (const item of document.cookie.split(';')) {
    const [name, ...rest] = item.trim().split('=')
    if (name === 'lcm_csrf') return decodeURIComponent(rest.join('='))
  }
  return ''
}

export function useManagerApi(fetcher: typeof $fetch = $fetch) {
  const config = useRuntimeConfig()
  const requestURL = useRequestURL()
  const apiBase = computed(() => {
    const configured = String(config.public.apiBase || '').replace(/\/$/, '')
    if (configured) return configured
    return `${requestURL.protocol}//${requestURL.hostname}:8888`
  })

  async function request<T = unknown>(path: string, options: any = {}): Promise<T> {
    const method = String(options.method || 'GET').toUpperCase()
    const headers = new Headers(options.headers || {})
    if (mutationMethods.has(method)) {
      const csrfToken = csrfTokenFromCookie()
      if (csrfToken) headers.set('X-CSRF-Token', csrfToken)
    }
    return await fetcher<T>(`${apiBase.value}${path}`, {
      credentials: 'include',
      ...options,
      headers
    })
  }

  return { apiBase, request }
}
