export type FrontendBuildEnvironment = {
  NUXT_PUBLIC_API_BASE?: string
  LLAMARACK_EXTERNAL_URL?: string
}

export function resolveFrontendApiBase(env: FrontendBuildEnvironment): string {
  return env.NUXT_PUBLIC_API_BASE || env.LLAMARACK_EXTERNAL_URL || ''
}
