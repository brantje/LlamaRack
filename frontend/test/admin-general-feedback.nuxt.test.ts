import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

describe('Administration General feedback rules', () => {
  it('keeps locked provenance readable and provides explicit save-state affordances', () => {
    const field = readFileSync(resolve(process.cwd(), 'app/components/AdminSettingField.vue'), 'utf8')
    expect(field).toContain('flex-col items-start gap-1')
    expect(field).toContain('source: {{ source }}')

    const page = readFileSync(resolve(process.cwd(), 'app/pages/admin/general.vue'), 'utf8')
    expect(page).not.toContain("? 'opacity-45' : ''")
    expect(page).toContain('No changes to save.')
    expect(page).toContain('data-testid="admin-general-save-top"')
    expect(page).toContain('data-testid="admin-general-save-bottom"')
    expect(page).toContain('data-testid="admin-general-mobile-actions"')
  })
})
