import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

const instancesSource = readFileSync(resolve(process.cwd(), 'app/pages/instances/index.vue'), 'utf8')

describe('Instances responsive header', () => {
  it('stacks page actions below the header copy on narrow screens', () => {
    const source = readFileSync(resolve(process.cwd(), 'app/pages/instances/index.vue'), 'utf8')
    expect(source).toContain('flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between')
    expect(source).toContain('w-full flex-wrap items-center justify-start gap-2 sm:w-auto sm:shrink-0 sm:justify-end')
  })
})

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
    expect(instancesSource).toContain('data-testid="instance-card-more"')
    expect(instancesSource).toContain('data-testid="instance-table-more"')
    expect(instancesSource).toContain('cardOverflowItems')
  })
})
