import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

const formSource = readFileSync(resolve(process.cwd(), 'app/components/InstanceForm.vue'), 'utf8')
const editSource = readFileSync(resolve(process.cwd(), 'app/pages/instances/[id]/edit.vue'), 'utf8')

describe('Instance form redesign cleanup', () => {
  it('uses semantic Frame and StatusTag error notes', () => {
    expect(formSource).toContain('data-testid="instance-form-error"')
    expect(formSource).toContain('<StatusTag variant="failed">Unable to save Instance</StatusTag>')
    expect(formSource).not.toContain('<UAlert')

    expect(editSource).toContain('data-testid="instance-edit-load-error"')
    expect(editSource).toContain('<StatusTag variant="failed">Unable to load Instance</StatusTag>')
    expect(editSource).not.toContain('<UAlert')
  })

  it('stacks the Back action below Instance form header copy on narrow screens', () => {
    expect(formSource).toContain('flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between')
    expect(formSource).toContain('w-full sm:w-auto sm:shrink-0')
  })

  it('retains the issue-required identity and placement hooks', () => {
    expect(formSource).toContain('data-testid="instance-name"')
    expect(formSource).toContain('data-testid="instance-slug"')
    expect(formSource).toContain("{ key: 'mmproj', label: 'Vision projector'")
    expect(formSource).toContain(':data-testid="`companion-${definition.key}`"')
    expect(formSource).toContain('data-testid="manual-placement-controls"')
    expect(formSource).toContain(':disabled="!canSubmit || submitDisabled"')
    expect(formSource).toContain('data-testid="instance-dirty-state"')
    expect(formSource).not.toContain("'border-[var(--color-divider)] opacity-60'")
    expect(editSource).toContain("if (!instance?.name && !instance?.id) throw")
    expect(editSource).toContain('submit-disabled-reason="No changes to save."')
    expect(editSource).toContain('const hasChanges = computed')
  })
})
