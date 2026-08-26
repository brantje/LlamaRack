import { readdirSync, readFileSync } from 'node:fs'
import { join, relative, resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

function vueFiles(directory: string): string[] {
  return readdirSync(directory, { withFileTypes: true }).flatMap(entry => {
    const path = join(directory, entry.name)
    if (entry.isDirectory()) return vueFiles(path)
    return entry.isFile() && entry.name.endsWith('.vue') ? [path] : []
  })
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

    if (/^<UEmpty\b/.test(tag) && cardDepth > 0 && !/\bvariant=["']naked["']/.test(tag)) {
      violations.push(`surfaced UEmpty inside card: ${tag}`)
    }
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
    const violations = vueFiles(appRoot).flatMap(file =>
      flatCardViolations(readFileSync(file, 'utf8')).map(message => `${relative(appRoot, file)}: ${message}`)
    )

    expect(violations).toEqual([])
  })

  it('keeps the model fleet as sibling model cards with a page-level log modal', () => {
    const source = readFileSync(resolve(process.cwd(), 'app/pages/models/index.vue'), 'utf8')
    expect(source).toContain('data-testid="model-card"')
    expect(source).toMatch(/grid gap-4 md:grid-cols-2 2xl:grid-cols-3/)
    expect(source).not.toMatch(/<UCard>[\s\S]*data-testid="model-card"/)
    expect(source).toContain('<UModal')
  })

  it('keeps Nuxt file-based pages mounted unconditionally', () => {
    const appRoot = resolve(process.cwd(), 'app')
    const appSource = readFileSync(resolve(appRoot, 'app.vue'), 'utf8')
    const vueSources = vueFiles(appRoot).map(file => ({ file, source: readFileSync(file, 'utf8') }))
    const layoutsRoot = resolve(appRoot, 'layouts')
    const configSource = readFileSync(resolve(process.cwd(), 'nuxt.config.ts'), 'utf8')

    expect(appSource.match(/<NuxtPage\b/g) || []).toHaveLength(1)
    expect(conditionalMountViolations(appSource, 'NuxtPage')).toEqual([])

    const routerViewFiles = vueSources
      .filter(({ source }) => /<RouterView\b/.test(source))
      .map(({ file }) => relative(appRoot, file))
    expect(routerViewFiles).toEqual([])

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
})
