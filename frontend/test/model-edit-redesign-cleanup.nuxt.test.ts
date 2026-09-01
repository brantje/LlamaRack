import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

const formSource = readFileSync(resolve(process.cwd(), 'app/components/ModelForm.vue'), 'utf8')
const editSource = readFileSync(resolve(process.cwd(), 'app/pages/models/[id]/edit.vue'), 'utf8')

describe('Edit model semantic cleanup', () => {
  it('uses the shared failed-note treatment for load and save errors', () => {
    expect(formSource).toContain("'model-edit-error'")
    expect(formSource).toContain("<StatusTag variant=\"failed\">{{ mode === 'edit' ? 'Model update failed' : 'Unable to complete model operation' }}</StatusTag>")
    expect(formSource).not.toContain('border-[var(--accent-800)]')
    expect(formSource).not.toContain('text-[var(--accent-900)]')
    expect(formSource).not.toContain('<UAlert')

    expect(editSource).toContain('data-testid="model-edit-error"')
    expect(editSource).toContain('<StatusTag variant="failed">Unable to load Model</StatusTag>')
    expect(editSource).not.toContain('border-[var(--accent-800)]')
    expect(editSource).not.toContain('<UAlert')
  })

  it('keeps the two Model-only peer surfaces and update contract', () => {
    expect(formSource).toContain("'model-edit-metadata'")
    expect(formSource).toContain("'model-edit-defaults'")
    expect(formSource).toContain('<LlamaCppOptionsEditor v-model="form.options" scope="model" :model-id="modelId" :exclude-keys="companionOptionKeys" />')
    expect(editSource).toContain("if (!model?.name) throw")
    expect(editSource).toContain('<ModelForm')
    expect(editSource).toContain('body: { name: form.name, context_length: form.context_length, options: form.options }')
    expect(editSource).toContain("await router.push('/models')")
    expect(editSource).not.toContain('always_on')
    expect(editSource).not.toContain('autoload')
    expect(editSource).not.toContain('eviction')
  })
})
