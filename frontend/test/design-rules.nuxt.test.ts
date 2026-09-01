import { readdirSync, readFileSync } from 'node:fs'
import { join, relative, resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

function sourceFiles(directory: string, extensions: string[] = ['.vue']): string[] {
  return readdirSync(directory, { withFileTypes: true }).flatMap(entry => {
    const path = join(directory, entry.name)
    if (entry.isDirectory()) return sourceFiles(path, extensions)
    return entry.isFile() && extensions.some(extension => entry.name.endsWith(extension)) ? [path] : []
  })
}

function vueFiles(directory: string) {
  return sourceFiles(directory, ['.vue'])
}

function flatCardViolations(source: string): string[] {
  const violations: string[] = []
  const tokens = source.matchAll(/<\/?(?:UCard|UPageCard)\b[^>]*>|<UEmpty\b[^>]*>/g)
  let cardDepth = 0

  for (const match of tokens) {
    const tag = match[0]
    if (/^<\/(?:UCard|UPageCard)/.test(tag)) {
      cardDepth = Math.max(0, cardDepth - 1)
      continue
    }
    if (/^<(?:UCard|UPageCard)\b/.test(tag)) {
      if (cardDepth > 0) violations.push(`nested card: ${tag}`)
      if (!/\/\s*>$/.test(tag)) cardDepth += 1
      continue
    }
    if (/^<UEmpty\b/.test(tag) && cardDepth > 0 && !/\bvariant=["']naked["']/.test(tag)) violations.push(`surfaced UEmpty inside card: ${tag}`)
  }
  return violations
}

function conditionalMountViolations(source: string, target: 'NuxtPage' | 'slot'): string[] {
  const clean = source.replace(/<!--[\s\S]*?-->/g, '')
  const tags = clean.matchAll(/<\/?[A-Za-z][A-Za-z0-9.-]*(?:\s[^<>]*?)?\/?>/g)
  const stack: Array<{ name: string; conditional: boolean }> = []
  const violations: string[] = []

  for (const match of tags) {
    const tag = match[0]
    const name = tag.match(/^<\/?([A-Za-z][A-Za-z0-9.-]*)/)?.[1]
    if (!name) continue
    if (tag.startsWith('</')) {
      const index = stack.map(entry => entry.name).lastIndexOf(name)
      if (index >= 0) stack.length = index
      continue
    }
    const ancestorConditional = stack.some(entry => entry.conditional)
    const ownConditional = /\bv-(?:if|else-if|else)(?:\s|=|>)/.test(tag)
    const conditional = ancestorConditional || ownConditional
    const isDefaultSlot = name === 'slot' && !/\bname\s*=/.test(tag)
    const isTarget = target === 'NuxtPage' ? name === 'NuxtPage' : isDefaultSlot
    if (isTarget && conditional) violations.push(tag)
    if (!/\/\s*>$/.test(tag)) stack.push({ name, conditional })
  }
  return violations
}

describe('frontend design rules', () => {
  it('keeps card hierarchy flat', () => {
    const appRoot = resolve(process.cwd(), 'app')
    const violations = vueFiles(appRoot).flatMap(file => flatCardViolations(readFileSync(file, 'utf8')).map(message => `${relative(appRoot, file)}: ${message}`))
    expect(violations).toEqual([])
  })

  it('uses Nuxt UI modals instead of native confirmation dialogs', () => {
    const appRoot = resolve(process.cwd(), 'app')
    const nativeConfirmation = /\b(?:(?:window|globalThis)\.)?confirm\s*\(/g
    const violations = sourceFiles(appRoot, ['.vue', '.ts']).flatMap(file => {
      const matches = readFileSync(file, 'utf8').match(nativeConfirmation) || []
      return matches.map(() => relative(appRoot, file))
    })
    expect(violations).toEqual([])
    expect(readFileSync(resolve(appRoot, 'components/AppConfirmationModal.vue'), 'utf8')).toContain('<UModal')
  })

  it('keeps Models as registry rows and Instances as table-first sibling runtime views', () => {
    const models = readFileSync(resolve(process.cwd(), 'app/pages/models/index.vue'), 'utf8')
    expect(models).toContain('data-testid="models-table"')
    expect(models).toContain('data-testid="model-row"')
    expect(models).not.toContain('data-testid="model-card"')
    expect(models).not.toMatch(/>Start<|>Stop<|>Logs</)

    const instances = readFileSync(resolve(process.cwd(), 'app/pages/instances/index.vue'), 'utf8')
    expect(instances).toContain("const viewMode = ref<ViewMode>('table')")
    expect(instances).toContain('data-testid="instances-table-view"')
    expect(instances).toContain('data-testid="instance-card"')
    expect(instances).toMatch(/grid gap-5 md:grid-cols-2 2xl:grid-cols-3/)
    expect(instances).toContain('<UModal')
    expect(instances).toContain('>Launch</AppButton>')
    expect(instances).toContain('>Stop</AppButton>')
    expect(instances).toContain('>Logs</AppButton>')
  })

  it('keeps Nuxt file-based pages mounted unconditionally', () => {
    const appRoot = resolve(process.cwd(), 'app')
    const appSource = readFileSync(resolve(appRoot, 'app.vue'), 'utf8')
    const vueSources = vueFiles(appRoot).map(file => ({ file, source: readFileSync(file, 'utf8') }))
    const layoutsRoot = resolve(appRoot, 'layouts')
    const configSource = readFileSync(resolve(process.cwd(), 'nuxt.config.ts'), 'utf8')

    expect(appSource.match(/<NuxtPage\b/g) || []).toHaveLength(1)
    expect(conditionalMountViolations(appSource, 'NuxtPage')).toEqual([])
    expect(vueSources.filter(({ source }) => /<RouterView\b/.test(source)).map(({ file }) => relative(appRoot, file))).toEqual([])

    const layoutViolations = vueFiles(layoutsRoot).flatMap(file => {
      const source = readFileSync(file, 'utf8')
      const defaultSlots = source.match(/<slot\b(?![^>]*\bname\s*=)[^>]*>/g) || []
      const conditionalSlots = conditionalMountViolations(source, 'slot')
      const messages: string[] = []
      if (defaultSlots.length !== 1) messages.push(`expected exactly one default <slot />, found ${defaultSlots.length}`)
      if (conditionalSlots.length) messages.push(`conditional default slot: ${conditionalSlots.join(', ')}`)
      return messages.map(message => `${relative(appRoot, file)}: ${message}`)
    })
    expect(layoutViolations).toEqual([])
    expect(configSource).not.toMatch(/\bpages\s*:\s*false\b/)
  })

  it('keeps redesign colors in complete registered theme files', () => {
    const appRoot = resolve(process.cwd(), 'app')
    const themeRoot = resolve(appRoot, 'themes')
    const registry = readFileSync(resolve(themeRoot, 'index.ts'), 'utf8')
    const requiredVariables = [
      '--color-bg', '--color-surface', '--color-text', '--color-accent', '--color-on-accent', '--color-divider',
      '--color-danger', '--color-on-danger', '--danger-100', '--danger-700',
      '--neutral-100', '--neutral-200', '--neutral-300', '--neutral-400', '--neutral-500', '--neutral-600', '--neutral-700', '--neutral-800', '--neutral-900',
      '--accent-100', '--accent-200', '--accent-300', '--accent-400', '--accent-500', '--accent-600', '--accent-700', '--accent-800', '--accent-900',
      '--shadow-sm', '--shadow-md', '--shadow-lg'
    ]

    expect(registry).toContain("DEFAULT_THEME_ID = 'dark'")
    expect(registry).toContain("id: 'dark'")
    expect(registry).toContain("id: 'light'")

    for (const name of ['dark.css', 'light.css']) {
      const theme = readFileSync(resolve(themeRoot, name), 'utf8')
      for (const variable of requiredVariables) expect(theme, `${name} missing ${variable}`).toContain(variable)
    }

    const rawThemeColors = /(?:^|[\s:'"(])#[0-9a-fA-F]{3,8}\b/gm
    const violations = sourceFiles(appRoot, ['.vue', '.ts', '.css'])
      .filter(file => !file.startsWith(themeRoot))
      .flatMap(file => (readFileSync(file, 'utf8').match(rawThemeColors) || []).map(value => `${relative(appRoot, file)}: ${value.trim()}`))
    expect(violations).toEqual([])
  })

  it('prohibits gradients and decorative blur effects in application UI', () => {
    const appRoot = resolve(process.cwd(), 'app')
    const forbidden = /linear-gradient\s*\(|radial-gradient\s*\(|conic-gradient\s*\(|\bbg-gradient-[^\s"']+|\bbackdrop-blur-[^\s"']+/g
    const violations = sourceFiles(appRoot, ['.vue', '.ts', '.css']).flatMap(file => {
      const matches = readFileSync(file, 'utf8').match(forbidden) || []
      return matches.map(match => `${relative(appRoot, file)}: ${match}`)
    })
    expect(violations).toEqual([])
  })

  it('does not alias warning and error semantics to the primary accent', () => {
    const config = readFileSync(resolve(process.cwd(), 'app/app.config.ts'), 'utf8')
    const css = readFileSync(resolve(process.cwd(), 'app/assets/css/main.css'), 'utf8')
    expect(config).toContain("warning: 'neutral'")
    expect(config).toContain("error: 'danger'")
    expect(config).not.toMatch(/warning:\s*'accent'[\s\S]*error:\s*'accent'/)
    expect(css).toContain('--color-danger-500: var(--color-danger)')
    expect(css).toContain('--color-danger-700: var(--danger-700)')
  })

  it('keeps destructive action tone separate from button priority', () => {
    const appRoot = resolve(process.cwd(), 'app')
    const legacyIntent = /<AppButton\b[^>]*\bintent=["']destructive["']/g
    const directDangerButton = /<UButton\b[^>]*(?:\bcolor=["'](?:error|warning)["']|:color=[^>]*(?:error|warning))/g
    const legacyIntentViolations = vueFiles(appRoot).flatMap(file => (readFileSync(file, 'utf8').match(legacyIntent) || []).map(match => `${relative(appRoot, file)}: ${match}`))
    const directDangerViolations = vueFiles(appRoot).flatMap(file => (readFileSync(file, 'utf8').match(directDangerButton) || []).map(match => `${relative(appRoot, file)}: ${match}`))
    expect(legacyIntentViolations).toEqual([])
    expect(directDangerViolations).toEqual([])

    const confirmationColorViolations = vueFiles(appRoot).flatMap(file => {
      const source = readFileSync(file, 'utf8')
      return [...source.matchAll(/confirmation\.value\?\.request\(\{[\s\S]*?\}\)/g)]
        .filter(match => /\bcolor\s*:/.test(match[0]))
        .map(() => relative(appRoot, file))
    })
    expect(confirmationColorViolations).toEqual([])

    const confirmationModal = readFileSync(resolve(appRoot, 'components/AppConfirmationModal.vue'), 'utf8')
    expect(confirmationModal).toContain('confirmTone')
    expect(confirmationModal).not.toContain('<UButton')
    const modelDeleteModal = readFileSync(resolve(appRoot, 'components/ModelDeleteModal.vue'), 'utf8')
    expect(modelDeleteModal).toContain('tone="destructive"')
    expect(modelDeleteModal).not.toContain('<UButton')
  })

  it('keeps redesign foundation surfaces on StatusTag and square flat primitives', () => {
    const appRoot = resolve(process.cwd(), 'app')
    const noBadgeOrAlert = [
      'components/HardwarePlacementEditor.vue',
      'components/LlamaCppOptionsEditor.vue',
      'components/InstanceRuntimeTelemetry.vue',
      'components/ModelDeleteModal.vue',
      'layouts/default.vue',
      'pages/admin/index.vue',
      'pages/admin/general.vue',
      'pages/admin/huggingface.vue',
      'pages/admin/llamacpp.vue',
      'pages/admin/system.vue',
      'pages/admin/users.vue'
    ]

    for (const path of noBadgeOrAlert) {
      const source = readFileSync(resolve(appRoot, path), 'utf8')
      expect(source, `${path} still uses UAlert`).not.toContain('<UAlert')
      if (path.includes('HardwarePlacementEditor') || path.includes('LlamaCppOptionsEditor') || path.includes('InstanceRuntimeTelemetry')) {
        expect(source, `${path} still uses UBadge`).not.toContain('<UBadge')
      }
    }

    for (const path of ['components/HardwarePlacementEditor.vue', 'components/LlamaCppOptionsEditor.vue']) {
      expect(readFileSync(resolve(appRoot, path), 'utf8'), `${path} still uses UCard option surfaces`).not.toContain('<UCard')
    }

    const instanceForm = readFileSync(resolve(appRoot, 'components/InstanceForm.vue'), 'utf8')
    const hardwarePlacement = readFileSync(resolve(appRoot, 'components/HardwarePlacementEditor.vue'), 'utf8')
    expect(instanceForm).toContain('<HardwarePlacementEditor')
    expect(instanceForm).toContain('hide-placement-controls')
    expect(instanceForm).not.toContain(':deep(')
    expect(hardwarePlacement, 'HardwarePlacementEditor is embedded inside the Placement Frame and must not create nested Frames').not.toContain('<Frame')

    const deleteModal = readFileSync(resolve(appRoot, 'components/ModelDeleteModal.vue'), 'utf8')
    expect(deleteModal).not.toContain('rounded-lg')

    const layout = readFileSync(resolve(appRoot, 'layouts/default.vue'), 'utf8')
    expect(layout).not.toContain('rounded-full')
    expect(layout).toContain('rounded-none')
  })

  it('quotes named machine fonts and keeps accent keyboard focus on form controls', () => {
    const shared = readFileSync(resolve(process.cwd(), 'app/themes/shared.css'), 'utf8')
    expect(shared).toContain("'SFMono-Regular'")
    expect(shared).toContain("'Menlo'")

    const main = readFileSync(resolve(process.cwd(), 'app/assets/css/main.css'), 'utf8')
    expect(main).toContain('--tw-ring-color: var(--color-accent)')
    expect(main).not.toContain('--tw-ring-color: var(--color-divider)')
  })
})
