import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

const source = readFileSync(resolve(process.cwd(), 'app/pages/playground.vue'), 'utf8')

describe('Playground redesign cleanup', () => {
  it('pins the chat composer to the bottom of the thread panel', () => {
    expect(source).toContain('data-testid="playground-composer"')
    expect(source).toContain('mt-auto shrink-0 border-t border-[var(--color-divider)]')
    expect(source).toContain('flex min-h-[calc(100dvh-12rem)] flex-col gap-4')
    expect(source).toContain('grid min-h-0 flex-1 gap-4')
  })

  it('uses native Nuxt UI chat components for the thread and composer', () => {
    expect(source).toContain('<UChatMessages')
    expect(source).toContain('<UChatReasoning')
    expect(source).toContain('<UChatPrompt')
    expect(source).toContain('<UChatPromptSubmit')
    expect(source).toContain('data-testid="playground-composer"')
    expect(source).toContain('data-testid="playground-attach-files"')
    expect(source).toContain('data-testid="playground-file-input"')
    expect(source).toContain('buildApiMessageContent')
  })

  it('uses the shared semantic request error note', () => {
    expect(source).toContain('data-testid="playground-error"')
    expect(source).toContain('<StatusTag variant="failed">Request error</StatusTag>')
    expect(source).not.toContain('bg-[var(--accent-100)] px-3 py-2 text-xs text-[var(--accent-900)]')
  })

  it('uses mono tabular typography for numeric parameters and diagnostics', () => {
    expect(source.match(/class="mt-1 font-mono tabular-nums"/g)).toHaveLength(7)
    expect(source).toContain('messageStats(message.id)')
    expect(source).toContain('class="mt-2 font-mono text-[length:var(--font-size-table-header)] tabular-nums')
    expect(source).toContain('Prompt tokens</dt><dd class="font-mono tabular-nums"')
    expect(source).toContain('Tokens / second</dt><dd class="font-mono tabular-nums"')
  })

  it('keeps mobile experiment controls next to the composer without duplicating the desktop Instance list', () => {
    expect(source).toContain('data-testid="playground-mobile-controls"')
    expect(source).toContain('aria-label="Playground Instance"')
    expect(source).toContain('data-testid="playground-mobile-parameters-toggle"')
    expect(source).toContain('data-testid="playground-mobile-quick-parameters"')
    expect(source).toContain('Quick controls stay beside the composer on mobile.')
    expect(source).toContain('hidden border-b border-[var(--color-divider)] p-4 xl:block')
    expect(source).toContain('text-[length:var(--font-size-h5)] font-semibold')
    expect(source).toContain("runtimeState === 'READY' ? 'Instance READY'")
    expect(source).toContain("failed: 'Last request failed'")
    expect(source).toContain('min-w-0 truncate font-mono')
    expect(source).toContain('text-[var(--color-on-accent)]')
    expect(source).not.toContain('bg-[var(--color-accent)] text-white')
  })

  it('uses the signed-in management bridge without a Playground API-key field', () => {
    expect(source).toContain("import { readManagementToken } from '~/composables/useManagerApi'")
    expect(source).toContain('/api/v1/playground/chat/completions')
    expect(source).not.toContain('playground-api-key')
    expect(source).not.toContain('lcm-playground-api-key')
    expect(source).not.toContain("const apiKey = ref('')")
  })
})
