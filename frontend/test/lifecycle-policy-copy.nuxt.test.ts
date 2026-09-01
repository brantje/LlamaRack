import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

const alwaysOnCopy = 'Keep this Instance running whenever resources permit.'
const evictionCopy = 'Allow the manager to stop this Instance when RAM/VRAM is needed for another Instance.'

function source(path: string) {
  return readFileSync(resolve(process.cwd(), path), 'utf8')
}

describe('Instance lifecycle policy copy', () => {
  it('keeps the shared New/Edit lifecycle controls visibly independent', () => {
    const form = source('app/components/InstanceForm.vue')
    expect(form).toContain('label="Always On"')
    expect(form).toContain(alwaysOnCopy)
    expect(form).toContain('label="Allow resource-pressure eviction"')
    expect(form).toContain(evictionCopy)

    expect(source('app/pages/instances/new.vue')).toContain('<InstanceForm')
    expect(source('app/pages/instances/[id]/edit.vue')).toContain('<InstanceForm')
  })

  it('keeps Model creation lifecycle copy independent', () => {
    const form = source('app/components/ModelForm.vue')
    expect(form).toContain('label="Always On"')
    expect(form).toContain(alwaysOnCopy)
    expect(form).toContain('label="Allow resource-pressure eviction"')
    expect(form).toContain(evictionCopy)

    expect(source('app/pages/models/new.vue')).toContain('<ModelForm')
    expect(source('app/pages/models/[id]/edit.vue')).toContain('<ModelForm')
  })
})
