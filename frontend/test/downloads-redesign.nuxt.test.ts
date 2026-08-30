import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import DownloadsPage from '~/pages/downloads.vue'
import { useManager } from '~/composables/useManager'

const mocks = vi.hoisted(() => ({ request: vi.fn() }))
mockNuxtImport('useManagerApi', () => () => ({ request: mocks.request, apiBase: { value: 'http://manager.test:8888' } }))

function resetManager() {
  const manager = useManager()
  manager.disconnectRuntimeEvents()
  manager.initialized.value = true
  manager.bootstrapRequired.value = false
  manager.backendError.value = ''
  manager.user.value = { id: 1, username: 'admin', enabled: true }
  manager.models.value = []
  manager.instances.value = []
  manager.runtimes.value = {}
  return manager
}

beforeEach(() => {
  mocks.request.mockReset()
  mocks.request.mockResolvedValue([])
  resetManager()
})

describe('downloads redesign', () => {
  it('uses the shared flat theme primitives instead of legacy colored/rounded treatments', () => {
    const source = readFileSync(resolve(process.cwd(), 'app/pages/downloads.vue'), 'utf8')

    expect(source).toContain('<StatusTag :variant="liveUpdates ? \'ready\' : \'pending\'">')
    expect(source).toContain('<Frame v-for="job in visibleJobs"')
    expect(source).toContain('data-testid="download-progress-track"')
    expect(source).toContain('bg-[var(--color-accent)]')
    expect(source).not.toContain('<UBadge')
    expect(source).not.toContain('<UAlert')
    expect(source).not.toContain('<UProgress')
    expect(source).not.toMatch(/rounded(?:-|\b)/)
    expect(source).not.toMatch(/(?:linear|radial|conic)-gradient|bg-gradient-|from-|via-|to-/)
  })

  it('maps active job and file states to shared status tags and keeps byte progress visible', async () => {
    const job = {
      id: 'job-1', provider: 'huggingface', repo_id: 'acme/model', revision: 'main', artifact_id: 'q4', name: 'model-q4.gguf',
      quantization: 'Q4_K_M', state: 'DOWNLOADING', total_bytes: 1024, downloaded_bytes: 512, speed_bps: 256, created_at: 1, updated_at: 2,
      files: [{ path: 'model-q4.gguf', local_path: '/models/model-q4.gguf.part', size: 1024, downloaded_bytes: 512, state: 'DOWNLOADING' }]
    }
    mocks.request.mockImplementation(async (path: string) => path === '/api/v1/downloads' ? [job] : [])

    const wrapper = await mountSuspended(DownloadsPage, { route: false })
    await flushPromises()

    expect(wrapper.text()).toContain('model-q4.gguf')
    expect(wrapper.text()).toContain('DOWNLOADING')
    expect(wrapper.text()).toContain('512 B / 1.0 KB · 50%')
    expect(wrapper.find('[data-testid="download-progress-fill"]').attributes('style')).toContain('width: 50%')
    expect(wrapper.find('[data-testid="download-job"]').exists()).toBe(true)
    wrapper.unmount()
  })

  it('maps completed, cancelled and failed states to the approved semantic variants', () => {
    const source = readFileSync(resolve(process.cwd(), 'app/pages/downloads.vue'), 'utf8')

    expect(source).toContain("if (state === 'COMPLETED') return 'ready'")
    expect(source).toContain("if (state === 'FAILED') return 'failed'")
    expect(source).toContain("if (state === 'CANCELLED') return 'neutral'")
    expect(source).toContain("return 'pending'")
  })
})
