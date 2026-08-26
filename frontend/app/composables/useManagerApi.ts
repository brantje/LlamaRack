export function useManagerApi() {
  const config = useRuntimeConfig()
  const requestURL = useRequestURL()
  const apiBase = computed(() => {
    const configured = String(config.public.apiBase || '').replace(/\/$/, '')
    if (configured) return configured
    return `${requestURL.protocol}//${requestURL.hostname}:8888`
  })

  async function request<T = unknown>(path: string, options: any = {}): Promise<T> {
    return await $fetch<T>(`${apiBase.value}${path}`, {
      credentials: 'include',
      ...options
    })
  }

  return { apiBase, request }
}
