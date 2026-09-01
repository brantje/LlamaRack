type ExternalIdentity = { provider_id: string; issuer: string }
type PublicOIDCProvider = { id: string; name: string }

export function profileDateTime(value?: number) {
  return value ? new Date(value * 1000).toLocaleString() : 'Never'
}

export function profileInitials(username: string) {
  const parts = username.trim().split(/[\s._-]+/).filter(Boolean)
  if (!parts.length) return '?'
  return parts.slice(0, 2).map(part => part[0]).join('').toUpperCase()
}

export function profileClientLabel(userAgent: string) {
  const ua = userAgent || ''
  const browser = /Firefox\//.test(ua) ? 'Firefox' : /Edg\//.test(ua) ? 'Edge' : /Chrome\//.test(ua) ? 'Chrome' : /Safari\//.test(ua) ? 'Safari' : 'Unknown client'
  const platform = /Windows/.test(ua) ? 'Windows' : /Mac OS X|Macintosh/.test(ua) ? 'macOS' : /Linux/.test(ua) ? 'Linux' : ''
  return platform && browser !== 'Unknown client' ? `${browser} on ${platform}` : browser
}

export function profileProviderName(identity: ExternalIdentity, authProviders: PublicOIDCProvider[]) {
  return authProviders.find(provider => provider.id === identity.provider_id)?.name || identity.issuer
}
