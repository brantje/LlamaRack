import { describe, expect, it } from 'vitest'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'

const source = readFileSync(resolve(process.cwd(), 'app/pages/models/[id]/edit.vue'), 'utf8')

describe('Edit model semantic cleanup', () => {
  it('uses the shared failed-note treatment for load and save errors', () => {
    expect(source).toContain('data-testid="model-edit-error"')
    expect(source).toContain('<StatusTag variant="failed">Model update failed</StatusTag>')
    expect(source).not.toContain('border-[var(--accent-800)]')
    expect(source).not.toContain('text-[var(--accent-900)]')
    expect(source).not.toContain('<UAlert')
  })

  it('keeps the two Model-only peer surfaces and update contract', () => {
    expect(source).toContain('data-testid="model-edit-metadata"')
    expect(source).toContain('data-testid="model-edit-defaults"')
    expect(source).toContain("if (!model?.name) throw")
    expect(source).toContain('<UForm v-else-if="loaded" :state="form"')
    expect(source).toContain('<LlamaCppOptionsEditor v-model="form.options" scope="model" :model-id="id" :exclude-keys="companionOptionKeys" />')
    expect(source).toContain('body: { name: form.name, context_length: form.context_length, options: form.options }')
    expect(source).toContain("await router.push('/models')")
    expect(source).not.toContain('always_on')
    expect(source).not.toContain('autoload')
    expect(source).not.toContain('eviction')
  })
})
