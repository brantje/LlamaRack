import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

const accountSource = readFileSync(resolve(process.cwd(), 'app/pages/profile/account.vue'), 'utf8')
const authenticationSource = readFileSync(resolve(process.cwd(), 'app/pages/profile/authentication.vue'), 'utf8')
const sessionsSource = readFileSync(resolve(process.cwd(), 'app/pages/profile/sessions.vue'), 'utf8')

describe('Profile machine typography', () => {
  it('uses mono tabular treatment for account timestamps', () => {
    expect(accountSource.match(/font-mono text-\[12px\] tabular-nums/g)).toHaveLength(2)
  })

  it('uses mono tabular treatment for authentication timestamps', () => {
    expect(authenticationSource).toContain('font-mono text-[10.5px] tabular-nums text-[var(--neutral-700)]">Linked {{ profileDateTime(identity.created_at) }}')
  })

  it('uses mono tabular treatment for session metadata', () => {
    expect(sessionsSource).toContain('break-words font-mono text-[10.5px] leading-5 tabular-nums text-[var(--neutral-700)]')
    expect(sessionsSource).toContain("{{ session.remote_address || 'Unknown address' }} · Created {{ profileDateTime(session.created_at) }} · Expires {{ profileDateTime(session.expires_at) }}")
  })
})
