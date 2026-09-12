import { expect, test, type Page, type Route, type TestInfo } from '@playwright/test'
import { prepareFullPageScreenshot } from './full-page-screenshot'

const blockedBenchmarkPages = new WeakSet<Page>()
const now = Date.parse('2026-09-09T12:00:00Z')
const timestamp = (seconds: number) => new Date(now + seconds * 1000).toISOString()

const model = {
  id: 'qwen3-8b-q4km',
  slug: 'qwen3-8b-q4km',
  name: 'Qwen3 8B',
  gguf_path: '/models/Qwen3-8B-Q4_K_M.gguf',
  total_bytes: 5_420_000_000,
  quantization: 'Q4_K_M',
  context_length: 32768,
  model_id: 'Qwen/Qwen3-8B-GGUF',
  enabled: true
}

const instance = {
  id: 'qwen3-primary',
  slug: 'qwen3-primary',
  model_id: model.id,
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
}

const hardware = {
  ram_total_bytes: 68_719_476_736,
  ram_available_bytes: 42_949_672_960,
  collected_at: timestamp(0),
  processes: [],
  gpus: [
    {
      id: 'cuda:0',
      backend: 'cuda',
      index: 0,
      name: 'NVIDIA GeForce RTX 4060 Ti',
      total_bytes: 17_179_869_184,
      used_bytes: 7_900_000_000,
      free_bytes: 9_279_869_184,
      utilization_pct: 54
    }
  ]
}

const defaultWorkload = {
  id: 'standard-v1',
  version: 1,
  prompt_tokens: [512, 2048],
  generation_tokens: [128],
  repetitions: 5,
  warmup: true
}

const capabilities = {
  available: true,
  version: 'b6124',
  fingerprint: 'fixture-llama-bench-b6124',
  output_format: 'json',
  supported_options: ['model', 'output', 'repetitions', 'n-prompt', 'n-gen'],
  workload: {
    version: 1,
    default: defaultWorkload,
    fields: [
      { key: 'prompt_tokens', label: 'Prompt tokens', kind: 'integer-list', minimum: 1, maximum: 1_000_000, description: 'Prompt-processing token counts.' },
      { key: 'generation_tokens', label: 'Generation tokens', kind: 'integer-list', minimum: 1, maximum: 1_000_000, description: 'Generation token counts.' },
      { key: 'repetitions', label: 'Repetitions', kind: 'integer', minimum: 1, maximum: 100, description: 'Measured repetitions per case.' },
      { key: 'warmup', label: 'Warm up', kind: 'boolean', description: 'Run a warm-up before measured repetitions.' }
    ]
  }
}

const baseRun = {
  instance_id: instance.id,
  instance_slug_snapshot: instance.slug,
  instance_name_snapshot: instance.name,
  instance_config_snapshot: {
    schema_version: 1,
    gpu_mode: 'auto',
    gpu_devices: ['cuda:0'],
    tensor_split: '',
    options: {
      'ctx-size': '8192',
      'n-gpu-layers': '99',
      'batch-size': '2048',
      threads: '12',
      'flash-attn': 'on'
    },
    sources: {
      'ctx-size': 'instance',
      'n-gpu-layers': 'instance',
      'batch-size': 'default',
      threads: 'instance',
      'flash-attn': 'instance'
    }
  },
  model_id: model.id,
  model_slug_snapshot: model.slug,
  model_name_snapshot: model.name,
  artifact_snapshot: {
    path: model.gguf_path,
    fingerprint: 'sha256:fixture-qwen3-q4km',
    size: model.total_bytes,
    quantization: model.quantization,
    architecture: 'qwen3',
    shard_count: 1,
    expected_shards: 1,
    files: [{ path: model.gguf_path, size: model.total_bytes, sha256: 'sha256:fixture-qwen3-q4km' }]
  },
  workload_profile: defaultWorkload,
  resolved_argv: [
    '/app/llama-bench', '-m', model.gguf_path, '-o', 'json', '-r', '5',
    '-p', '512,2048', '-n', '128', '--ctx-size', '8192', '--n-gpu-layers', '99',
    '--batch-size', '2048', '--threads', '12', '--flash-attn', 'on'
  ],
  mapping_differences: [],
  build: {
    llamarack_version: '1.1.0-dev',
    llamarack_commit: 'fixture199',
    runtime_variant: 'cuda',
    llama_cpp_release: 'b6124',
    llama_cpp_build: 'b6124',
    llama_bench_version: 'b6124',
    llama_bench_fingerprint: 'fixture-llama-bench-b6124'
  },
  hardware_snapshot: {
    observed: hardware,
    cpu: { model: 'AMD Ryzen 9 7950X', logical_threads: 32, architecture: 'amd64', os: 'linux' }
  },
  benchmark_schema_version: 1,
  parser_schema_version: 1
}

const completedA = {
  ...baseRun,
  id: 'bench-a',
  status: 'COMPLETED',
  created_at: timestamp(-7200),
  started_at: timestamp(-7198),
  completed_at: timestamp(-7168),
  results: [
    { case_index: 0, case_id: 'pp512', prompt_tokens: 512, generation_tokens: 0, repetitions: 5, average_tokens_per_second: 4100.2, stddev_tokens_per_second: 34.1 },
    { case_index: 1, case_id: 'pp2048', prompt_tokens: 2048, generation_tokens: 0, repetitions: 5, average_tokens_per_second: 3780.4, stddev_tokens_per_second: 29.8 },
    { case_index: 2, case_id: 'tg128', prompt_tokens: 0, generation_tokens: 128, repetitions: 5, average_tokens_per_second: 74.8, stddev_tokens_per_second: 0.9 }
  ]
}

const completedB = {
  ...baseRun,
  id: 'bench-b',
  status: 'COMPLETED',
  created_at: timestamp(-3600),
  started_at: timestamp(-3598),
  completed_at: timestamp(-3567),
  results: [
    { case_index: 0, case_id: 'pp512', prompt_tokens: 512, generation_tokens: 0, repetitions: 5, average_tokens_per_second: 4215.7, stddev_tokens_per_second: 31.2 },
    { case_index: 1, case_id: 'pp2048', prompt_tokens: 2048, generation_tokens: 0, repetitions: 5, average_tokens_per_second: 3899.1, stddev_tokens_per_second: 27.6 },
    { case_index: 2, case_id: 'tg128', prompt_tokens: 0, generation_tokens: 128, repetitions: 5, average_tokens_per_second: 76.4, stddev_tokens_per_second: 0.7 }
  ]
}

const unlikeHardware = {
  ...hardware,
  gpus: [{ ...hardware.gpus[0], name: 'NVIDIA GeForce RTX 4090', total_bytes: 25_769_803_776, free_bytes: 18_000_000_000 }]
}
const completedUnlike = {
  ...baseRun,
  id: 'bench-c',
  status: 'COMPLETED',
  created_at: timestamp(-1800),
  started_at: timestamp(-1798),
  completed_at: timestamp(-1765),
  artifact_snapshot: { ...baseRun.artifact_snapshot, fingerprint: 'sha256:different-artifact' },
  instance_config_snapshot: {
    ...baseRun.instance_config_snapshot,
    options: { ...baseRun.instance_config_snapshot.options, 'ctx-size': '16384' }
  },
  build: { ...baseRun.build, llama_cpp_build: 'b6130', llama_cpp_release: 'b6130' },
  hardware_snapshot: { ...baseRun.hardware_snapshot, observed: unlikeHardware },
  results: [
    { case_index: 0, case_id: 'pp512', prompt_tokens: 512, generation_tokens: 0, repetitions: 5, average_tokens_per_second: 5200.6, stddev_tokens_per_second: 41.3 },
    { case_index: 1, case_id: 'pp2048', prompt_tokens: 2048, generation_tokens: 0, repetitions: 5, average_tokens_per_second: 4920.9, stddev_tokens_per_second: 38.1 },
    { case_index: 2, case_id: 'tg128', prompt_tokens: 0, generation_tokens: 128, repetitions: 5, average_tokens_per_second: 91.2, stddev_tokens_per_second: 1.1 }
  ]
}

const runningRun = {
  ...baseRun,
  id: 'bench-running',
  status: 'RUNNING',
  created_at: timestamp(-90),
  started_at: timestamp(-82),
  results: []
}
const failedRun = {
  ...baseRun,
  id: 'bench-failed',
  status: 'FAILED',
  created_at: timestamp(-900),
  started_at: timestamp(-895),
  completed_at: timestamp(-891),
  failure: 'llama-bench exited with status 1',
  diagnostic_output: 'representative benchmark failure for visual QA',
  results: []
}

const benchmarkRuns = [runningRun, completedB, completedA, completedUnlike, failedRun]
const runByID = new Map(benchmarkRuns.map(run => [run.id, run]))
const corsHeaders = {
  'access-control-allow-origin': 'http://127.0.0.1:3000',
  'access-control-allow-headers': 'authorization,content-type',
  'access-control-allow-methods': 'GET,POST,PATCH,PUT,DELETE,OPTIONS',
  'content-type': 'application/json'
}

async function fulfill(route: Route, body: unknown, status = 200) {
  await route.fulfill({ status, headers: corsHeaders, body: JSON.stringify(body) })
}

async function installBenchmarkFixture(page: Page) {
  await page.addInitScript(() => {
    window.sessionStorage.setItem('llamarack_management_token', 'benchmark-visual-token')
    window.localStorage.setItem('llamarack-theme', 'dark')
    Object.defineProperty(window, 'WebSocket', { value: undefined, configurable: true })
  })

  await page.route('http://127.0.0.1:8888/**', async (route) => {
    const request = route.request()
    if (request.method() === 'OPTIONS') {
      await route.fulfill({ status: 204, headers: corsHeaders, body: '' })
      return
    }
    const url = new URL(request.url())
    const path = url.pathname
    const method = request.method()

    if (path === '/api/v1/auth/bootstrap') return fulfill(route, { required: false })
    if (path === '/api/v1/auth/providers') return fulfill(route, { local_login_enabled: true, providers: [] })
    if (path === '/api/v1/me') return fulfill(route, { id: 1, username: 'admin', enabled: true })
    if (path === '/api/v1/models') return fulfill(route, [model])
    if (path === '/api/v1/instances') return fulfill(route, [instance])
    if (path === `/api/v1/instances/${instance.slug}` && method === 'GET') return fulfill(route, instance)
    if (path === `/api/v1/instances/${instance.slug}/runtime`) return fulfill(route, { instance_id: instance.id, model_id: model.id, state: 'READY', pid: 1421, port: 11001, started_at: timestamp(-7200), ready_at: timestamp(-7180) })
    if (path === '/api/v1/llamacpp/profile') return fulfill(route, { available: true, profile: { path: '/app/llama-server', version: 'b6124', fingerprint: 'fixture-b6124', options: [] } })
    if (path === '/api/v1/auth/ws-ticket') return fulfill(route, { error: 'disabled in benchmark screenshot fixture' }, 503)
    if (path === '/api/v1/settings/general') return fulfill(route, { observability_retention_days: { value: 30, source: 'database', editable: true } })
    if (path === '/api/v1/observability/timeseries') return fulfill(route, { metric: url.searchParams.get('metric') || 'fixture', bucket_seconds: 60, items: [] })
    if (path === '/api/v1/models/inspect') return fulfill(route, { dependencies: [] })
    if (path === '/api/v1/hardware' || path === '/api/v1/hardware/snapshot') return fulfill(route, hardware)
    if (path === '/api/v1/llamacpp/config') return fulfill(route, {
      effective: {
        values: baseRun.instance_config_snapshot.options,
        sources: baseRun.instance_config_snapshot.sources
      },
      unsupported: []
    })
    if (path === '/api/v1/benchmarks/capabilities') {
      return fulfill(route, blockedBenchmarkPages.has(page)
        ? { ...capabilities, available: false, reason: 'The bundled llama-bench executable is unavailable for this runtime image.' }
        : capabilities)
    }
    if (path === '/api/v1/benchmarks' && method === 'GET') {
      const status = url.searchParams.get('status')
      const instanceID = url.searchParams.get('instance_id')
      const modelID = url.searchParams.get('model_id')
      const items = benchmarkRuns.filter(run => (!status || run.status === status) && (!instanceID || run.instance_id === instanceID) && (!modelID || run.model_id === modelID))
      return fulfill(route, { items, total: items.length, limit: Number(url.searchParams.get('limit') || 50), offset: Number(url.searchParams.get('offset') || 0) })
    }
    const benchmarkMatch = path.match(/^\/api\/v1\/benchmarks\/([^/]+)$/)
    if (benchmarkMatch && method === 'GET') {
      const run = runByID.get(decodeURIComponent(benchmarkMatch[1] || ''))
      return run ? fulfill(route, run) : fulfill(route, { error: 'benchmark not found' }, 404)
    }

    return fulfill(route, method === 'GET' ? {} : { ok: true })
  })
}

async function waitForManagerPanel(page: Page) {
  const panel = page.locator('#dashboard-panel-manager-main')
  await expect(panel).toBeVisible({ timeout: 15_000 })
  await expect(panel).not.toBeEmpty()
  await expect(page.getByRole('heading', { name: 'LlamaRack connection failed' })).toBeHidden()
  await expect(page.getByRole('heading', { name: 'Welcome back' })).toBeHidden()
}

async function openAuthenticated(page: Page, path: string) {
  await page.goto(path, { waitUntil: 'domcontentloaded' })
  await waitForManagerPanel(page)
}

async function captureBenchmarkScreenshot(page: Page, testInfo: TestInfo, name: string) {
  await page.waitForTimeout(600)
  const documentOverflow = await page.evaluate(() => Math.max(document.documentElement.scrollWidth, document.body.scrollWidth) - window.innerWidth)
  expect(documentOverflow, `${name} document should not overflow horizontally`).toBeLessThanOrEqual(1)
  await prepareFullPageScreenshot(page)
  await page.screenshot({
    path: `artifacts/ux-screenshots/${testInfo.project.name}/benchmark-${name}.png`,
    fullPage: true,
    animations: 'disabled'
  })
}

test.beforeEach(async ({ page }) => {
  await installBenchmarkFixture(page)
  await page.emulateMedia({ reducedMotion: 'reduce' })
})

test('benchmark history screenshot', async ({ page }, testInfo) => {
  await openAuthenticated(page, '/benchmarks')
  await expect(page.getByRole('heading', { name: 'Benchmark history' })).toBeVisible()
  const table = page.locator('[data-testid="benchmark-history-table"]')
  await expect(table).toContainText('COMPLETED')
  await expect(table).toContainText('RUNNING')
  await expect(table).toContainText('FAILED')
  await expect(table).toContainText('NVIDIA GeForce RTX 4060 Ti')
  await captureBenchmarkScreenshot(page, testInfo, 'history')
})

test('benchmark Instance modal ready screenshot', async ({ page }, testInfo) => {
  await openAuthenticated(page, `/instances/${instance.slug}/detail`)
  await page.locator('[data-testid="instance-benchmark-action"]').click()
  const dialog = page.getByRole('dialog', { name: `Benchmark ${instance.name}` })
  await expect(dialog).toBeVisible()
  await expect(dialog).toContainText('Instance configuration · read only')
  await expect(dialog).toContainText('Benchmark workload')
  await expect(dialog).toContainText('Hardware / admission')
  await expect(dialog).toContainText('NVIDIA GeForce RTX 4060 Ti')
  await expect(dialog.locator('[data-testid="run-benchmark"]')).toBeEnabled()
  await captureBenchmarkScreenshot(page, testInfo, 'instance-modal-ready')
})

test('benchmark Instance modal blocked screenshot', async ({ page }, testInfo) => {
  blockedBenchmarkPages.add(page)
  await openAuthenticated(page, `/instances/${instance.slug}/detail`)
  await page.locator('[data-testid="instance-benchmark-action"]').click()
  const dialog = page.getByRole('dialog', { name: `Benchmark ${instance.name}` })
  await expect(dialog).toContainText('Unavailable')
  await expect(dialog).toContainText('The bundled llama-bench executable is unavailable for this runtime image.')
  await expect(dialog.locator('[data-testid="run-benchmark"]')).toBeDisabled()
  await captureBenchmarkScreenshot(page, testInfo, 'instance-modal-blocked')
})

test('completed benchmark detail screenshot', async ({ page }, testInfo) => {
  await openAuthenticated(page, `/benchmarks/${completedA.id}`)
  await expect(page.getByText('COMPLETED', { exact: true })).toBeVisible()
  await expect(page.locator('[data-testid="benchmark-detail-summary"]')).toContainText('NVIDIA GeForce RTX 4060 Ti')
  await expect(page.getByRole('heading', { name: 'Measurements' })).toBeVisible()
  await expect(page.getByText('tg128', { exact: true })).toBeVisible()
  await captureBenchmarkScreenshot(page, testInfo, 'detail-completed')
})

test('like-for-like benchmark comparison screenshot', async ({ page }, testInfo) => {
  await openAuthenticated(page, `/benchmarks/compare?ids=${completedA.id},${completedB.id}`)
  const differences = page.locator('[data-testid="benchmark-comparison-differences"]')
  await expect(differences).toContainText('Like-for-like inputs')
  await expect(page.getByRole('heading', { name: 'Matching benchmark cases' })).toBeVisible()
  await expect(page.getByText('tg128', { exact: true })).toBeVisible()
  await captureBenchmarkScreenshot(page, testInfo, 'comparison-like-for-like')
})

test('unlike-input benchmark comparison screenshot', async ({ page }, testInfo) => {
  await openAuthenticated(page, `/benchmarks/compare?ids=${completedA.id},${completedUnlike.id}`)
  const differences = page.locator('[data-testid="benchmark-comparison-differences"]')
  await expect(differences).toContainText('Inputs differ')
  await expect(differences).toContainText('Model artifact')
  await expect(differences).toContainText('Changed setting')
  await expect(differences).toContainText('--ctx-size')
  await expect(differences).toContainText('8192')
  await expect(differences).toContainText('16384')
  await expect(differences).toContainText('GPU hardware')
  await expect(differences).toContainText('llama.cpp build')
  await expect(page.getByText('No overall winner is inferred when material inputs differ.')).toBeVisible()
  await captureBenchmarkScreenshot(page, testInfo, 'comparison-inputs-differ')
})
