import { describe, expect, it } from 'vitest'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'

describe('model edit redesign', () => {
  it('uses two peer framed sections and the shared action hierarchy', () => {
    const source = readFileSync(resolve(process.cwd(), 'app/pages/models/[id]/edit.vue'), 'utf8')

    expect(source).toContain('data-testid="model-edit-metadata"')
    expect(source).toContain('data-testid="model-edit-defaults"')
    expect(source).toContain('Reusable defaults inherited by every Instance of this Model unless that Instance overrides the flag.')
    expect(source).toContain('<AppButton to="/models" intent="secondary">Cancel</AppButton>')
    expect(source).toContain('data-testid="model-edit-submit-hint"')
    expect(source).toContain(':disabled="!canSubmit"')
    expect(source).toContain("'No changes to save.'")
    expect(source).toContain("'Unsaved changes.'")
    expect(source).not.toContain('<UCard')
    expect(source).not.toContain('<UAlert')
    expect(source).not.toMatch(/rounded(?:-|\b)/)
    expect(source).not.toMatch(/(?:linear|radial|conic)-gradient|bg-gradient-|from-|via-|to-/)
  })

  it('keeps model-only fields and the existing update payload', () => {
    const source = readFileSync(resolve(process.cwd(), 'app/pages/models/[id]/edit.vue'), 'utf8')

    expect(source).toContain("reactive({ name: '', context_length: 0, options: {} as Record<string, string> })")
    expect(source).toContain('body: { name: form.name, context_length: form.context_length, options: form.options }')
    expect(source).not.toContain('always_on')
    expect(source).not.toContain('autoload')
    expect(source).not.toContain('gpu_mode')
    expect(source).not.toContain('eviction')
  })
})
