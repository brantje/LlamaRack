import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mockNuxtImport, mountSuspended } from '@nuxt/test-utils/runtime'
import AdminSystemPage from '~/pages/admin/system.vue'
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
  manager.profile.value = null
}

function systemResponse(identity: Record<string, any>) {
  return {
    identity,
    manager: { uptime_seconds: 42, runtime: { data_dir: '/config' } },
    network: { effective_scheme: 'https', secure_cookie: true, allowed_origins: '', trusted_proxies: '', external_url: '' },
    llamacpp: { available: true, path: '/app/llama-server', version: 'b9999', options: 123 }
  }
}

beforeEach(() => {
  mocks.request.mockReset()
  resetManager()
})

describe('Administration System build identity', () => {
  it('renders exact official release and runtime identity', async () => {
    const commit = '1234567890abcdef1234567890abcdef12345678'
    mocks.request.mockResolvedValue(systemResponse({
      version: '1.0.0-rc.1',
      commit,
      build_time: '2026-09-04T20:04:23Z',
      channel: 'release',
      variant: 'cuda',
      dirty: false,
      llama_cpp: {
        release: 'b6124',
        build: 'b6124',
        image: 'ghcr.io/ggml-org/llama.cpp:server-cuda-b6124'
      }
    }))

    const wrapper = await mountSuspended(AdminSystemPage, { route: '/admin/system' })
    await flushPromises()

    const identity = wrapper.get('[data-testid="admin-system-identity"]')
    expect(identity.text()).toContain('Build identity')
    expect(identity.text()).toContain('1.0.0-rc.1')
    expect(identity.text()).toContain('Release')
    expect(identity.text()).toContain('CUDA')
    expect(identity.get('[data-testid="build-commit"]').text()).toBe('1234567890ab')
    expect(identity.get('[data-testid="build-commit"]').attributes('title')).toBe(commit)
    expect(identity.get('button[aria-label^="Copy commit"]').attributes('aria-label')).toContain(commit)
    expect(identity.text()).toContain('UTC')
    expect(identity.text()).toContain('b6124')
    expect(identity.text()).toContain('ghcr.io/ggml-org/llama.cpp:server-cuda-b6124')
    expect(identity.text()).not.toContain('Modified')
    wrapper.unmount()
  })

  it('makes development and incomplete metadata explicit without fake values', async () => {
    mocks.request.mockResolvedValue(systemResponse({
      version: 'development',
      channel: 'development',
      variant: 'unknown',
      dirty: true,
      llama_cpp: {}
    }))

    const wrapper = await mountSuspended(AdminSystemPage, { route: '/admin/system' })
    await flushPromises()

    const identity = wrapper.get('[data-testid="admin-system-identity"]')
    expect(identity.text()).toContain('Development')
    expect(identity.text()).toContain('Modified')
    expect(identity.text()).toContain('Unknown')
    expect(identity.find('[data-testid="build-commit"]').text()).toBe('Unknown')
    expect(identity.find('button[aria-label^="Copy commit"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('preserves custom variants and invalid timestamps as diagnostic text', async () => {
    mocks.request.mockResolvedValue(systemResponse({
      version: 'custom-build',
      channel: 'custom',
      variant: 'vulkan',
      build_time: 'builder-clock-unavailable',
      llama_cpp: {}
    }))

    const wrapper = await mountSuspended(AdminSystemPage, { route: '/admin/system' })
    await flushPromises()

    const identity = wrapper.get('[data-testid="admin-system-identity"]')
    expect(identity.text()).toContain('Custom')
    expect(identity.text()).toContain('Vulkan')
    expect(identity.text()).toContain('builder-clock-unavailable')
    wrapper.unmount()
  })
})
