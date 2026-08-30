import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

const profileSource = readFileSync(resolve(process.cwd(), 'app/pages/profile.vue'), 'utf8')

describe('Profile machine typography', () => {
  it('uses mono tabular treatment for account and authentication timestamps', () => {
    expect(profileSource.match(/font-mono text-\[12px\] tabular-nums/g)).toHaveLength(2)
    expect(profileSource).toContain('font-mono text-[10.5px] tabular-nums text-[var(--neutral-700)]">Linked {{ dateTime(identity.created_at) }}')
  })

  it('uses mono tabular treatment for session metadata', () => {
    expect(profileSource).toContain('break-words font-mono text-[10.5px] leading-5 tabular-nums text-[var(--neutral-700)]')
    expect(profileSource).toContain("{{ session.remote_address || 'Unknown address' }} · Created {{ dateTime(session.created_at) }} · Expires {{ dateTime(session.expires_at) }}")
  })
})
