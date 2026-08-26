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

describe('frontend design rules', () => {
  it('keeps card hierarchy flat', () => {
    const appRoot = resolve(process.cwd(), 'app')
    const violations = vueFiles(appRoot).flatMap(file =>
      flatCardViolations(readFileSync(file, 'utf8')).map(message => `${relative(appRoot, file)}: ${message}`)
    )

    expect(violations).toEqual([])
  })
})
