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

  it('keeps Models as registry rows and Instances as sibling runtime cards', () => {
    const models = readFileSync(resolve(process.cwd(), 'app/pages/models/index.vue'), 'utf8')
    expect(models).toContain('data-testid="models-table"')
    expect(models).toContain('data-testid="model-row"')
    expect(models).not.toContain('data-testid="model-card"')
    expect(models).not.toMatch(/>Start<|>Stop<|>Logs</)

    const instances = readFileSync(resolve(process.cwd(), 'app/pages/instances/index.vue'), 'utf8')
    expect(instances).toContain('data-testid="instance-card"')
    expect(instances).toMatch(/grid gap-4 md:grid-cols-2 2xl:grid-cols-3/)
    expect(instances).toContain('<UModal')
    expect(instances).toContain('>Launch</UButton>')
    expect(instances).toContain('>Stop</UButton>')
    expect(instances).toContain('>Logs</UButton>')
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
      '--color-bg', '--color-surface', '--color-text', '--color-accent', '--color-divider',
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
})
