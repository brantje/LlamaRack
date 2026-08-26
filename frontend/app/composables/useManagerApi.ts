export function useManagerApi() {
  const config = useRuntimeConfig()
  const apiBase = computed(() => String(config.public.apiBase || '').replace(/\/$/, ''))

  async function request<T = unknown>(path: string, options: any = {}): Promise<T> {
    return await $fetch<T>(`${apiBase.value}${path}`, {
      credentials: 'include',
      ...options
    })
  }

  return { apiBase, request }
}
