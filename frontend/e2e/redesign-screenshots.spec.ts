import { expect, test, type Page, type Route } from '@playwright/test'

const now = Date.now()
const nowSeconds = Math.floor(now / 1000)

const models = [
  {
    id: 'qwen3-8b-q4km',
    name: 'Qwen3 8B',
    gguf_path: '/models/Qwen3-8B-Q4_K_M.gguf',
    total_bytes: 5_420_000_000,
    quantization: 'Q4_K_M',
    context_length: 32768,
    model_id: 'Qwen/Qwen3-8B-GGUF',
    enabled: true,
    autoload_enabled: true,
    always_on: false,
    priority: 'normal',
    eviction_enabled: true,
    idle_unload_seconds: 900,
    routing_policy: 'least-loaded'
  },
  {
    id: 'gemma-3-12b-q5km',
    name: 'Gemma 3 12B',
    gguf_path: '/models/gemma-3-12b-it-Q5_K_M.gguf',
    total_bytes: 8_230_000_000,
    quantization: 'Q5_K_M',
    context_length: 65536,
    model_id: 'google/gemma-3-12b-it-GGUF',
    enabled: true,
    autoload_enabled: false,
    always_on: true,
    priority: 'high',
    eviction_enabled: false,
    idle_unload_seconds: 0,
    routing_policy: 'round-robin'
  }
]

const instances = [
  {
    id: 'qwen3-primary',
    model_id: 'qwen3-8b-q4km',
    name: 'Qwen3 primary',
    enabled: true,
    autoload_enabled: true,
    always_on: false,
    priority: 'normal',
    eviction_enabled: true,
    idle_unload_seconds: 900,
    gpu_mode: 'auto',
    gpu_devices: ['cuda:0'],
    tensor_split: '',
    request_log_mode: 'metadata'
  },
  {
    id: 'gemma-always-on',
    model_id: 'gemma-3-12b-q5km',
    name: 'Gemma always-on',
    enabled: true,
    autoload_enabled: false,
    always_on: true,
    priority: 'high',
    eviction_enabled: false,
    idle_unload_seconds: 0,
    gpu_mode: 'manual',
    gpu_devices: ['cuda:1'],
    tensor_split: '1.0',
    request_log_mode: 'full'
  }
]

const runtimes: Record<string, Record<string, unknown>> = {
  'qwen3-primary': {
    instance_id: 'qwen3-primary', model_id: 'qwen3-8b-q4km', state: 'READY', pid: 1421, port: 11001
  },
  'gemma-always-on': {
    instance_id: 'gemma-always-on', model_id: 'gemma-3-12b-q5km', state: 'READY', pid: 1428, port: 11002
  }
}

const hardware = {
  ram_total_bytes: 68_719_476_736,
  ram_available_bytes: 42_949_672_960,
  collected_at: new Date(now).toISOString(),
  processes: [],
  gpus: [
    { id: 'cuda:0', backend: 'cuda', index: 0, name: 'NVIDIA GeForce RTX 4060 Ti', total_bytes: 17_179_869_184, used_bytes: 7_900_000_000, free_bytes: 9_279_869_184, utilization_pct: 54 },
    { id: 'cuda:1', backend: 'cuda', index: 1, name: 'NVIDIA GeForce RTX 4060 Ti', total_bytes: 17_179_869_184, used_bytes: 10_800_000_000, free_bytes: 6_379_869_184, utilization_pct: 71 }
  ]
}

const telemetry = [
  { instance_id: 'qwen3-primary', pid: 1421, gpu_devices: ['cuda:0'], gpus: [{ device_id: 'cuda:0', vram_used_bytes: 7_200_000_000, utilization_pct: 54 }], vram_used_bytes: 7_200_000_000, gpu_utilization_pct: 54, cpu_percent: 18.2, memory_used_bytes: 2_100_000_000, collected_at: new Date(now).toISOString() },
  { instance_id: 'gemma-always-on', pid: 1428, gpu_devices: ['cuda:1'], gpus: [{ device_id: 'cuda:1', vram_used_bytes: 10_100_000_000, utilization_pct: 71 }], vram_used_bytes: 10_100_000_000, gpu_utilization_pct: 71, cpu_percent: 24.6, memory_used_bytes: 3_400_000_000, collected_at: new Date(now).toISOString() }
]

const requests = [
  { id: 501, request_id: 'req_a1b2c3', accepted_at: now - 18_000, started_at: now - 17_700, finished_at: now - 15_300, instance_id: 'qwen3-primary', endpoint: '/v1/chat/completions', api_key: { id: 'key-default', name: 'Open WebUI', prefix: 'lcm_sk_ab12' }, streaming: true, status_code: 200, result: 'success', duration_ms: 2400, ttft_ms: 186, prompt_tokens: 814, generated_tokens: 246, total_tokens: 1060, tokens_per_second: 61.4, queue_duration_ms: 18, load_duration_ms: 0, autoloaded: false },
  { id: 500, request_id: 'req_d4e5f6', accepted_at: now - 54_000, started_at: now - 53_600, finished_at: now - 50_100, instance_id: 'gemma-always-on', endpoint: '/v1/responses', api_key: { id: 'key-ci', name: 'Evaluation', prefix: 'lcm_sk_cd34' }, streaming: false, status_code: 200, result: 'success', duration_ms: 3500, ttft_ms: 242, prompt_tokens: 1280, generated_tokens: 302, total_tokens: 1582, tokens_per_second: 48.1, queue_duration_ms: 31, load_duration_ms: 0, autoloaded: false }
]

const apiKeys = [
  { id: 'key-default', name: 'Open WebUI', prefix: 'lcm_sk_ab12', enabled: true, created_at: nowSeconds - 86400 * 21, last_used_at: nowSeconds - 18 },
  { id: 'key-ci', name: 'Evaluation', prefix: 'lcm_sk_cd34', enabled: false, created_at: nowSeconds - 86400 * 8, last_used_at: nowSeconds - 3600 }
]

const user = { id: 1, username: 'admin', enabled: true }
const corsHeaders = {
  'access-control-allow-origin': 'http://127.0.0.1:3000',
  'access-control-allow-headers': 'authorization,content-type',
  'access-control-allow-methods': 'GET,POST,PATCH,PUT,DELETE,OPTIONS',
  'content-type': 'application/json'
}

function responseFor(pathname: string, method: string): unknown {
  if (pathname === '/api/v1/auth/bootstrap') return { required: false }
  if (pathname === '/api/v1/auth/providers') return { local_login_enabled: true, providers: [{ id: 'authentik', name: 'Authentik' }] }
  if (pathname === '/api/v1/me') return user
  if (pathname === '/api/v1/models' && method === 'GET') return models
  if (pathname === '/api/v1/instances' && method === 'GET') return instances
  if (/^\/api\/v1\/instances\/[^/]+\/runtime$/.test(pathname)) return runtimes[decodeURIComponent(pathname.split('/')[4] || '')] || { state: 'UNLOADED' }
  if (pathname === '/api/v1/auth/ws-ticket') return { error: 'disabled in screenshot fixture' }
  if (pathname === '/api/v1/llamacpp/profile') return {
    available: true,
    profile: {
      path: '/usr/local/bin/llama-server', version: 'b6124', fingerprint: 'fixture-b6124',
      options: [
        { key: 'ctx-size', value_hint: 'N', description: 'Size of the prompt context', kind: 'number' },
        { key: 'parallel', value_hint: 'N', description: 'Number of parallel sequences', kind: 'number' },
        { key: 'flash-attn', description: 'Enable Flash Attention', kind: 'boolean' }
      ]
    }
  }
  if (pathname === '/api/v1/settings/general') return {
    idle_unload_seconds: { value: 900, source: 'database', editable: true },
    observability_retention_days: { value: 30, source: 'database', editable: true },
    external_url: { value: 'http://127.0.0.1:8888', source: 'environment', editable: false },
    cors_allowed_origins: { value: 'http://127.0.0.1:3000', source: 'database', editable: true }
  }
  if (pathname === '/api/v1/observability/summary') return {
    since: now - 900_000, requests: 1842, successes: 1829, errors: 13, active: 3, queued: 1, active_api_keys: 2,
    prompt_tokens: 1_488_420, generated_tokens: 624_980, total_tokens: 2_113_400,
    lifecycle: { autoloads: 7, failed_starts: 1, load_duration_ms_total: 38_400 },
    hardware: { hardware, telemetry }
  }
  if (pathname === '/api/v1/observability/requests') return { items: requests, next_cursor: '' }
  if (pathname === '/api/v1/hardware' || pathname === '/api/v1/hardware/snapshot') return hardware
  if (pathname === '/api/v1/api-keys' && method === 'GET') return apiKeys
  if (pathname === '/api/v1/system') return {
    manager: { uptime_seconds: 104_822, runtime: { go_version: 'go1.25.0', os: 'linux', arch: 'amd64' } },
    network: { effective_scheme: 'http', secure_cookie: false, allowed_origins: { value: 'http://127.0.0.1:3000' }, trusted_proxies: { value: '127.0.0.1/32' }, external_url: { value: 'http://127.0.0.1:8888' } },
    llamacpp: { available: true, path: '/usr/local/bin/llama-server', version: 'b6124', fingerprint: 'fixture-b6124', options: 146 }
  }
  if (/\/users(?:\/|$)/.test(pathname)) return [user, { id: 2, username: 'operator', enabled: true }]
  if (/\/api-keys(?:\/|$)/.test(pathname)) return apiKeys
  if (/\/downloads(?:\/|$)/.test(pathname)) return []
  if (/\/observability\/requests\//.test(pathname)) return requests[0]
  if (/\/logs(?:\/|$)/.test(pathname)) return { items: [] }
  if (/\/huggingface(?:\/|$)/.test(pathname)) return { enabled: true, token_configured: true, items: [] }
  if (/\/discover(?:\/|$)/.test(pathname) || /\/repositories(?:\/|$)/.test(pathname)) return { items: [] }
  if (/\/settings(?:\/|$)/.test(pathname)) return {}
  if (/\/auth\/oidc(?:\/|$)/.test(pathname)) return []
  return method === 'GET' ? {} : { ok: true }
}

async function installApiFixture(page: Page) {
  await page.addInitScript(() => {
    window.sessionStorage.setItem('lcm_management_token', 'ux-review-token')
    window.localStorage.setItem('llamacpp-manager-theme', 'dark')
  })

  await page.route('http://127.0.0.1:8888/**', async (route: Route) => {
    const request = route.request()
    if (request.method() === 'OPTIONS') {
      await route.fulfill({ status: 204, headers: corsHeaders, body: '' })
      return
    }
    const url = new URL(request.url())
    if (url.pathname === '/api/v1/auth/ws-ticket') {
      await route.fulfill({ status: 503, headers: corsHeaders, body: JSON.stringify(responseFor(url.pathname, request.method())) })
      return
    }
    await route.fulfill({ status: 200, headers: corsHeaders, body: JSON.stringify(responseFor(url.pathname, request.method())) })
  })
}

const pages = [
  ['dashboard', '/'],
  ['instances', '/instances'],
  ['instance-new', '/instances/new'],
  ['instance-detail', '/instances/qwen3-primary/detail'],
  ['instance-edit', '/instances/qwen3-primary/edit'],
  ['models', '/models'],
  ['model-new', '/models/new'],
  ['model-details', '/models/qwen3-8b-q4km/details'],
  ['model-edit', '/models/qwen3-8b-q4km/edit'],
  ['models-discover', '/models/discover'],
  ['model-repository', '/models/discover/Qwen/Qwen3-8B-GGUF'],
  ['downloads', '/downloads'],
  ['playground', '/playground'],
  ['request-logs', '/logs'],
  ['api-keys', '/api'],
  ['profile', '/profile'],
  ['admin-overview', '/admin'],
  ['admin-authentication', '/admin/authentication'],
  ['admin-general', '/admin/general'],
  ['admin-huggingface', '/admin/huggingface'],
  ['admin-llamacpp', '/admin/llamacpp'],
  ['admin-logs', '/admin/logs'],
  ['admin-system', '/admin/system'],
  ['admin-users', '/admin/users']
] as const

test.beforeEach(async ({ page }) => {
  await installApiFixture(page)
  await page.emulateMedia({ reducedMotion: 'reduce' })
})

for (const [name, path] of pages) {
  test(`${name} screenshot`, async ({ page }, testInfo) => {
    await page.goto(path, { waitUntil: 'domcontentloaded' })
    await expect(page.locator('body')).not.toContainText('Manager connection failed')
    await expect(page.locator('body')).not.toContainText('Welcome back')
    await page.waitForTimeout(800)
    await page.screenshot({
      path: `artifacts/ux-screenshots/${testInfo.project.name}/${name}.png`,
      fullPage: true,
      animations: 'disabled'
    })
  })
}
