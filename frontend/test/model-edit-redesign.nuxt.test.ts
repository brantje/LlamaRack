import { describe, expect, it } from 'vitest'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'

const formSource = readFileSync(resolve(process.cwd(), 'app/components/ModelForm.vue'), 'utf8')
const editSource = readFileSync(resolve(process.cwd(), 'app/pages/models/[id]/edit.vue'), 'utf8')
const newSource = readFileSync(resolve(process.cwd(), 'app/pages/models/new.vue'), 'utf8')

describe('model edit redesign', () => {
  it('uses three peer framed sections and the shared action hierarchy', () => {
    expect(formSource).toContain("'model-edit-metadata'")
    expect(formSource).toContain("'model-edit-companions'")
    expect(formSource).toContain("'model-edit-defaults'")
    expect(formSource).toContain('Reusable defaults inherited by every Instance of this Model unless that Instance overrides the flag.')
    expect(formSource).toContain('<AppButton :to="backTo" intent="secondary">Cancel</AppButton>')
    expect(formSource).toContain("'model-edit-submit-hint'")
    expect(formSource).toContain(':disabled="submitBlocked"')
    expect(formSource).toContain("'No changes to save.'")
    expect(formSource).toContain("'Unsaved changes.'")
    expect(formSource).not.toContain('<UCard')
    expect(formSource).not.toContain('<UAlert')
    expect(formSource).not.toMatch(/rounded(?:-|\b)/)
    expect(formSource).not.toMatch(/(?:linear|radial|conic)-gradient|bg-gradient-|from-|via-|to-/)
  })

  it('keeps model-only fields and the existing update payload', () => {
    expect(editSource).toContain("reactive({ name: '', context_length: 0, options: {} as Record<string, string> })")
    expect(editSource).toContain('body: { name: form.name, context_length: form.context_length, options: form.options }')
    expect(editSource).toContain('<ModelForm')
    expect(newSource).toContain('<ModelForm')
    expect(editSource).not.toContain('always_on')
    expect(editSource).not.toContain('autoload')
    expect(editSource).not.toContain('gpu_mode')
    expect(editSource).not.toContain('eviction')
  })
})
