import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

const source = readFileSync(resolve(process.cwd(), 'app/pages/playground.vue'), 'utf8')

describe('Playground redesign cleanup', () => {
  it('pins the chat composer to the bottom of a viewport-locked thread', () => {
    expect(source).toContain('data-testid="playground-composer"')
    expect(source).toContain('mt-auto shrink-0 border-t border-[var(--color-divider)]')
    expect(source).toContain('flex h-[calc(100dvh-8.5rem)] min-h-0 flex-col gap-3 overflow-hidden lg:h-[calc(100dvh-5rem)]')
    expect(source).toContain('grid min-h-0 flex-1 gap-4 overflow-y-auto xl:grid-cols-[minmax(0,1fr)_24rem] xl:items-stretch xl:overflow-hidden')
    expect(source).toContain('flex min-h-[calc(100dvh-15rem)] min-w-0 flex-col overflow-hidden p-0 xl:h-full xl:min-h-0')
    expect(source).toContain('xl:overflow-y-auto')
    expect(source).toContain("root: 'min-h-0 w-full flex-1 overflow-y-auto px-2.5 [&>article]:last-of-type:min-h-0'")
  })

  it('uses native Nuxt UI chat components for the thread and composer', () => {
    expect(source).toContain('<UChatMessages')
    expect(source).toContain('<UChatReasoning')
    expect(source).toContain('<UChatPrompt')
    expect(source).toContain('<UChatPromptSubmit')
    expect(source).toContain('<UFormField label="temperature"')
    expect(source).toContain('<UFormField label="system prompt"')
    expect(source).toContain('buildApiMessageContent')
    expect(source).toContain('<AppConfirmationModal')
    expect(source).toContain('data-testid="playground-composer"')
    expect(source).toContain('>Clear chat</AppButton>')
    expect(source).toContain('data-testid="playground-attach-files"')
    expect(source).toContain('data-testid="playground-file-input"')
    expect(source).toContain('<UTabs')
    expect(source).toContain(':unmount-on-hide="false"')
    expect(source).toContain('<UTextarea')
    expect(source).toContain('placeholder="token, or one per line"')
    expect(source).toContain("'aria-label': 'Scroll to latest messages'")
    expect(source).toContain('Stop generating')
    expect(source).toContain('X-LiteLLM-Session-ID')
    expect(source).toContain('data-testid="playground-reuse-session"')
    expect(source).toContain('data-testid="playground-session-id"')
    expect(source).toContain('data-testid="playground-diagnostics-session"')
  })

  it('uses the shared semantic request error note', () => {
    expect(source).toContain('data-testid="playground-error"')
    expect(source).toContain('<StatusTag variant="failed">Request error</StatusTag>')
    expect(source).toContain('whitespace-pre-wrap break-words')
    expect(source).not.toContain('class="flex shrink-0 items-start gap-2 p-3"')
    expect(source).not.toContain('bg-[var(--accent-100)] px-3 py-2 text-xs text-[var(--accent-900)]')
  })

  it('uses mono tabular typography for numeric parameters and diagnostics', () => {
    expect(source.match(/class="font-mono tabular-nums"/g)).toHaveLength(18)
    expect(source).toContain('messageStats(message.id)')
    expect(source).toContain('class="mt-2 font-mono text-[length:var(--font-size-table-header)] tabular-nums')
    expect(source).toContain('Prompt tokens</dt><dd class="font-mono tabular-nums"')
    expect(source).toContain('Tokens / second</dt><dd class="font-mono tabular-nums"')
    expect(source).toContain('completion (incl. reasoning)')
  })

  it('keeps instance and runtime chrome on the thread without duplicating parameter controls', () => {
    expect(source).toContain('data-testid="playground-thread-chrome"')
    expect(source).toContain('aria-label="Playground Instance"')
    expect(source).not.toContain('data-testid="playground-mobile-parameters-toggle"')
    expect(source).not.toContain('data-testid="playground-mobile-quick-parameters"')
    expect(source).not.toContain('Quick controls stay beside the composer on mobile.')
    expect(source).toContain('hidden border-b border-[var(--color-divider)] p-4 xl:block')
    expect(source).toContain('data-testid="playground-instance-list"')
    expect(source).toContain('Instance — the OpenAI model value')
    expect(source).toContain("runtimeState === 'READY' ? 'Instance READY'")
    expect(source).toContain("failed: 'Last request failed'")
    expect(source).toContain('min-w-0 truncate font-mono')
    expect(source).toContain('text-[var(--color-on-accent)]')
    expect(source).not.toContain('bg-[var(--color-accent)] text-white')
    expect(source).toContain('Type a prompt to start.')
    expect(source).not.toContain('title="Exercise an Instance through the real gateway."')
  })

  it('uses the signed-in management bridge without a Playground API-key field', () => {
    expect(source).toContain("import { readManagementToken } from '~/composables/useManagerApi'")
    expect(source).toContain('/api/v1/playground/chat/completions')
    expect(source).not.toContain('playground-api-key')
    expect(source).not.toContain('lcm-playground-api-key')
    expect(source).not.toContain("const apiKey = ref('')")
  })

  it('themes ChatPrompt as an opaque square surface without glass', () => {
    const config = readFileSync(resolve(process.cwd(), 'app/app.config.ts'), 'utf8')
    expect(config).toContain('chatPrompt:')
    expect(config).toContain('bg-[var(--color-surface)]')
    expect(config).toContain('ring ring-[var(--color-divider)]')
    expect(config).toContain('has-[textarea:focus-visible]:outline-none')
    expect(config).not.toContain('bg-default/75')
    expect(source).toContain('chatPromptUi')
    expect(source).not.toContain('backdrop-blur')
    const css = readFileSync(resolve(process.cwd(), 'app/assets/css/main.css'), 'utf8')
    expect(css).toContain("form:has(textarea:focus-visible)")
    expect(css).toContain('--tw-ring-color: var(--color-accent) !important')
  })
})
