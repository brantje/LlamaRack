import { describe, expect, it } from 'vitest'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'

describe('remaining redesign surfaces', () => {
  it('keeps Authentication inside the Administration shell and semantic design system', () => {
    const page = readFileSync(resolve(process.cwd(), 'app/pages/admin/authentication.vue'), 'utf8')
    const nav = readFileSync(resolve(process.cwd(), 'app/components/navigation/AdminSidebar.vue'), 'utf8')

    expect(nav).toContain("label: 'Authentication'")
    expect(nav).toContain("to: '/admin/authentication'")
    expect(page).toContain('<AdminShell title="Authentication"')
    expect(page).toContain('data-testid="authentication-sign-in-policy"')
    expect(page).toContain('data-testid="authentication-providers"')
    expect(page).toContain('<StatusTag :variant="provider.enabled ? \'ready\' : \'neutral\'"')
    expect(page).toContain('intent="primary" :loading="savingSettings" :disabled="!settings"')
    expect(page).not.toContain('<UAlert')
    expect(page).not.toContain('<UBadge')
  })

  it('uses the flat semantic repository summary for Discover', () => {
    const header = readFileSync(resolve(process.cwd(), 'app/components/ModelsDiscoverRepositoryHeader.vue'), 'utf8')

    expect(header).toContain('<Frame class="p-5" data-testid="discover-repository-header">')
    expect(header).toContain('<StatusTag v-if="model.private" variant="neutral">Private</StatusTag>')
    expect(header).toContain('<StatusTag v-if="model.gated" variant="neutral">Gated</StatusTag>')
    expect(header).not.toContain('<UCard')
    expect(header).not.toContain('<UBadge')
    expect(header).not.toMatch(/rounded-|bg-gradient-|linear-gradient|radial-gradient|conic-gradient/)
  })
})
