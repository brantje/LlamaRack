import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

function variable(source: string, name: string) {
  const value = source.match(new RegExp(`${name}\\s*:\\s*(#[0-9a-fA-F]{6})`))?.[1]
  if (!value) throw new Error(`Missing ${name}`)
  return value
}

function luminance(hex: string) {
  const channels = [1, 3, 5].map(index => Number.parseInt(hex.slice(index, index + 2), 16) / 255)
  const linear = channels.map(value => value <= 0.04045 ? value / 12.92 : ((value + 0.055) / 1.055) ** 2.4)
  return 0.2126 * linear[0]! + 0.7152 * linear[1]! + 0.0722 * linear[2]!
}

function contrast(a: string, b: string) {
  const first = luminance(a)
  const second = luminance(b)
  const high = Math.max(first, second)
  const low = Math.min(first, second)
  return (high + 0.05) / (low + 0.05)
}

describe('redesign feedback regressions', () => {
  it('keeps accent and muted text contrast at WCAG AA normal-text levels', () => {
    for (const name of ['dark.css', 'light.css']) {
      const source = readFileSync(resolve(process.cwd(), `app/themes/${name}`), 'utf8')
      expect(contrast(variable(source, '--color-accent'), variable(source, '--color-on-accent')), `${name} on-accent`).toBeGreaterThanOrEqual(4.5)
      expect(contrast(variable(source, '--color-danger'), variable(source, '--color-on-danger')), `${name} on-danger`).toBeGreaterThanOrEqual(4.5)
      expect(contrast(variable(source, '--color-bg'), variable(source, '--danger-700')), `${name} danger on page`).toBeGreaterThanOrEqual(4.5)
      expect(contrast(variable(source, '--color-surface'), variable(source, '--danger-700')), `${name} danger on surface`).toBeGreaterThanOrEqual(4.5)
      expect(contrast(variable(source, '--color-bg'), variable(source, '--neutral-700')), `${name} muted on page`).toBeGreaterThanOrEqual(4.5)
      expect(contrast(variable(source, '--color-surface'), variable(source, '--neutral-700')), `${name} muted on surface`).toBeGreaterThanOrEqual(4.5)
    }
    expect(readFileSync(resolve(process.cwd(), 'app/assets/css/main.css'), 'utf8')).toContain('--ui-text-inverted: var(--color-on-accent)')
  })

  it('keeps reviewed mobile action hierarchy and overflow affordances', () => {
    const adminShell = readFileSync(resolve(process.cwd(), 'app/components/AdminShell.vue'), 'utf8')
    const adminNav = readFileSync(resolve(process.cwd(), 'app/components/navigation/AdminSidebar.vue'), 'utf8')
    const huggingFace = readFileSync(resolve(process.cwd(), 'app/pages/admin/huggingface.vue'), 'utf8')
    const authentication = readFileSync(resolve(process.cwd(), 'app/pages/admin/authentication.vue'), 'utf8')
    const api = readFileSync(resolve(process.cwd(), 'app/pages/api.vue'), 'utf8')
    const instanceForm = readFileSync(resolve(process.cwd(), 'app/components/InstanceForm.vue'), 'utf8')
    const instances = readFileSync(resolve(process.cwd(), 'app/pages/instances/index.vue'), 'utf8')
    const modelNew = readFileSync(resolve(process.cwd(), 'app/pages/models/new.vue'), 'utf8')
    const discover = readFileSync(resolve(process.cwd(), 'app/components/ModelsDiscover.vue'), 'utf8')
    const users = readFileSync(resolve(process.cwd(), 'app/pages/admin/users.vue'), 'utf8')
    const dashboard = readFileSync(resolve(process.cwd(), 'app/pages/index.vue'), 'utf8')
    const logs = readFileSync(resolve(process.cwd(), 'app/pages/admin/system-logs.vue'), 'utf8')
    expect(adminShell).toContain('flex flex-col items-stretch gap-4 sm:flex-row')
    expect(adminNav).toContain('i-lucide-chevron-down')
    expect(huggingFace).not.toContain('<template #actions>')
    expect(huggingFace.match(/@click="save"/g) || []).toHaveLength(1)
    expect(authentication).toContain('intent="secondary" icon="i-lucide-plus"')
    expect(authentication).toContain('Scroll horizontally for issuer, callback and actions.')
    expect(api).toContain('data-testid="copy-api-base-url"')
    expect(api).toContain('class="min-w-[760px]"')
    expect(instanceForm).toContain('data-testid="instance-submit-requirements"')
    expect(instanceForm).toContain('text-[var(--color-on-accent)]')
    expect(instances).toContain('Instances table. Scroll horizontally for telemetry, lifecycle and actions.')
    expect(instances).toContain(":color=\"stateFilter === filter.value ? 'primary' : 'neutral'\"")
    expect(instances).toContain(":variant=\"stateFilter === filter.value ? 'soft' : 'ghost'\"")
    expect(modelNew).toContain('data-testid="model-submit-requirements"')
    expect(discover).toContain('Results update automatically as you type. Press Enter to search immediately.')
    expect(discover).not.toContain('>Search</AppButton>')
    expect(users).toContain('Administration users. Scroll horizontally for status, dates and actions.')
    expect(dashboard).toContain('Gateway traffic. Scroll horizontally for request metrics and result.')
    expect(logs).toContain("'WARN+'")
    expect(logs).toContain(":color=\"selectedSource === source ? 'primary' : 'neutral'\"")
    expect(logs).toContain(":variant=\"selectedSource === source ? 'soft' : 'ghost'\"")
  })

  it('distinguishes empty model metadata from an empty search result', () => {
    const source = readFileSync(resolve(process.cwd(), 'app/pages/models/[id]/details.vue'), 'utf8')
    expect(source).toContain('No GGUF metadata returned for this artifact')
    expect(source).toContain('No keys match')
    expect(source).toContain('v-if="details?.metadata_total"')
  })
})
