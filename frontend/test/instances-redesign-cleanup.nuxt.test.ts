import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

const instancesSource = readFileSync(resolve(process.cwd(), 'app/pages/instances/index.vue'), 'utf8')

describe('Instances redesign cleanup', () => {
  it('uses the semantic error note instead of UAlert', () => {
    expect(instancesSource).toContain('data-testid="instances-error"')
    expect(instancesSource).toContain('<StatusTag variant="failed">Instance operation failed</StatusTag>')
    expect(instancesSource).not.toContain('<UAlert')
  })

  it('retains the issue-required table, card and identity hooks', () => {
    expect(instancesSource).toContain("const viewMode = ref<ViewMode>('table')")
    expect(instancesSource).toContain("const viewStorageKey = 'llamacpp-manager.instances.view'")
    expect(instancesSource).toContain('data-testid="instance-card"')
    expect(instancesSource).toContain('data-testid="instance-id"')
    expect(instancesSource).toContain('data-testid="copy-instance-id"')
  })
})
