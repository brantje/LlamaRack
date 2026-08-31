import { describe, expect, it } from 'vitest'
import { profileClientLabel, profileDateTime, profileInitials, profileProviderName } from '~/utils/profileDisplay'

describe('profileDisplay', () => {
  it('formats timestamps, initials and client labels', () => {
    expect(profileDateTime()).toBe('Never')
    expect(profileDateTime(10)).toBe(new Date(10_000).toLocaleString())
    expect(profileInitials('john.doe')).toBe('JD')
    expect(profileInitials('')).toBe('?')
    expect(profileClientLabel('Chrome/100 Mac OS X')).toBe('Chrome on macOS')
    expect(profileClientLabel('')).toBe('Unknown client')
  })

  it('resolves provider names with issuer fallback', () => {
    const identity = { provider_id: 'authentik', issuer: 'https://auth.example.test/' }
    expect(profileProviderName(identity, [{ id: 'authentik', name: 'Authentik' }])).toBe('Authentik')
    expect(profileProviderName(identity, [])).toBe('https://auth.example.test/')
  })
})
