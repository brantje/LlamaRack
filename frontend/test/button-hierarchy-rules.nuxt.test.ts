import { readdirSync, readFileSync } from 'node:fs'
import { join, relative, resolve } from 'node:path'
import { NodeTypes, parse, type ElementNode, type TemplateChildNode } from '@vue/compiler-dom'
import { describe, expect, it } from 'vitest'

function vueFiles(directory: string): string[] {
  return readdirSync(directory, { withFileTypes: true }).flatMap(entry => {
    const path = join(directory, entry.name)
    if (entry.isDirectory()) return vueFiles(path)
    return entry.isFile() && entry.name.endsWith('.vue') ? [path] : []
  })
}

function staticIntent(node: ElementNode) {
  for (const prop of node.props) {
    if (prop.type === NodeTypes.ATTRIBUTE && prop.name === 'intent') return prop.value?.content || ''
    if (prop.type === NodeTypes.DIRECTIVE && prop.name === 'bind' && prop.arg?.type === NodeTypes.SIMPLE_EXPRESSION && prop.arg.content === 'intent') {
      const value = prop.exp?.type === NodeTypes.SIMPLE_EXPRESSION ? prop.exp.content.trim() : ''
      if (value === "'primary'" || value === '"primary"') return 'primary'
    }
  }
  return ''
}

function multiplePrimaryGroups(source: string): number {
  const template = source.match(/<template>([\s\S]*?)<\/template>/)?.[1]
  if (!template) return 0
  const root = parse(template)
  let violations = 0

  function visit(node: TemplateChildNode) {
    if (node.type !== NodeTypes.ELEMENT) return
    const buttons = node.children.filter((child): child is ElementNode => child.type === NodeTypes.ELEMENT && child.tag === 'AppButton')
    if (buttons.length >= 2 && buttons.filter(button => staticIntent(button) === 'primary').length > 1) violations += 1
    for (const child of node.children) visit(child)
  }

  for (const child of root.children) visit(child)
  return violations
}

describe('button hierarchy rules', () => {
  it('maps priority to the required Nuxt UI variants independently of tone', () => {
    const source = readFileSync(resolve(process.cwd(), 'app/components/AppButton.vue'), 'utf8')
    expect(source).toContain("if (props.intent === 'primary') return { color: 'primary' as const, variant: 'solid' as const")
    expect(source).toContain("if (props.intent === 'ghost') return { color: 'neutral' as const, variant: 'ghost' as const")
    expect(source).toContain("return { color: 'neutral' as const, variant: 'outline' as const, class: '' }")
    expect(source).toContain("if (props.tone === 'destructive')")
    expect(source).toContain("if (props.intent === 'primary') return {")
    expect(source).toContain("variant: 'solid' as const")
    expect(source).toContain("bg-[var(--color-danger)] text-[var(--color-on-danger)]")
    expect(source).toContain("variant: 'outline' as const")
    expect(source).toContain("variant: 'ghost' as const")
  })

  it('keeps confirmation cancel secondary and destructive confirmation primary', () => {
    const shared = readFileSync(resolve(process.cwd(), 'app/components/AppConfirmationModal.vue'), 'utf8')
    expect(shared).toContain('data-testid="confirmation-cancel" intent="secondary"')
    expect(shared).toContain(':intent="dialog.confirmIntent" :tone="dialog.confirmTone"')
    expect(shared).toContain("confirmIntent: options.confirmIntent || 'primary'")

    const modelDelete = readFileSync(resolve(process.cwd(), 'app/components/ModelDeleteModal.vue'), 'utf8')
    expect(modelDelete).toContain('data-testid="confirmation-cancel" intent="secondary"')
    expect(modelDelete).toContain('data-testid="confirmation-confirm" intent="primary" tone="destructive"')
  })

  it('allows at most one static primary action in each direct action group', () => {
    const appRoot = resolve(process.cwd(), 'app')
    const violations = vueFiles(appRoot).flatMap(file => {
      const count = multiplePrimaryGroups(readFileSync(file, 'utf8'))
      return Array.from({ length: count }, () => relative(appRoot, file))
    })
    expect(violations).toEqual([])
  })
})
