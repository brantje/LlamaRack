import { expect, test, type Page, type Route } from '@playwright/test'

const now = Date.now()
const nowSeconds = Math.floor(now / 1000)
const playgroundHoldRelease = new WeakMap<Page, () => void>()
const playgroundColdPages = new WeakSet<Page>()
const instancesStatePages = new WeakSet<Page>()
const dashboardFailurePages = new WeakSet<Page>()
const discoverDetailFailurePages = new WeakSet<Page>()
const modelInspectFailurePages = new WeakSet<Page>()
const modelDetailsFailurePages = new WeakSet<Page>()
const modelDetailsPageTwoPages = new WeakSet<Page>()

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
  },
  {
    id: 'qwen3-failed', model_id: 'qwen3-8b-q4km', name: 'Qwen3 failed', enabled: true, autoload_enabled: true,
    always_on: false, priority: 'normal', eviction_enabled: true, idle_unload_seconds: 900, gpu_mode: 'auto', gpu_devices: ['cuda:0'], tensor_split: '', request_log_mode: 'metadata'
  },
  {
    id: 'qwen3-unloaded', model_id: 'qwen3-8b-q4km', name: 'Qwen3 unloaded', enabled: true, autoload_enabled: true,
    always_on: false, priority: 'normal', eviction_enabled: true, idle_unload_seconds: 900, gpu_mode: 'auto', gpu_devices: ['cuda:0'], tensor_split: '', request_log_mode: 'metadata'
  },
  {
    id: 'qwen3-downloading', model_id: 'qwen3-8b-q4km', name: 'Qwen3 downloading', enabled: true, autoload_enabled: true,
    always_on: false, priority: 'normal', eviction_enabled: true, idle_unload_seconds: 900, gpu_mode: 'auto', gpu_devices: ['cuda:0'], tensor_split: '', request_log_mode: 'metadata'
  }
]

const runtimes: Record<string, Record<string, unknown>> = {
  'qwen3-primary': {
    instance_id: 'qwen3-primary', model_id: 'qwen3-8b-q4km', state: 'READY', pid: 1421, port: 11001
  },
  'gemma-always-on': {
    instance_id: 'gemma-always-on', model_id: 'gemma-3-12b-q5km', state: 'READY', pid: 1428, port: 11002
  },
  'qwen3-failed': {
    instance_id: 'qwen3-failed', model_id: 'qwen3-8b-q4km', state: 'FAILED', last_error: 'CUDA out of memory while allocating the KV cache.'
  },
  'qwen3-unloaded': {
    instance_id: 'qwen3-unloaded', model_id: 'qwen3-8b-q4km', state: 'UNLOADED'
  },
  'qwen3-downloading': {
    instance_id: 'qwen3-downloading', model_id: 'qwen3-8b-q4km', state: 'UNLOADED'
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
  { id: 501, request_id: 'req_a1b2c3', trace_id: 'trace_fixture', session_id: 'session_fixture', session_total_count: 2, model_name: 'Qwen3 8B', call_type: 'chat_completion', request_body: '{\"messages\":[{\"role\":\"user\",\"content\":\"Explain KV cache reuse\"}]}', response_body: '{\"choices\":[{\"message\":{\"role\":\"assistant\",\"content\":\"Reuse avoids repeated prompt evaluation.\"}}]}', accepted_at: now - 18_000, started_at: now - 17_700, finished_at: now - 15_300, instance_id: 'qwen3-primary', endpoint: '/v1/chat/completions', api_key: { id: 'key-default', name: 'Open WebUI', prefix: 'lcm_sk_ab12' }, streaming: true, status_code: 200, result: 'success', duration_ms: 2400, ttft_ms: 186, prompt_tokens: 814, generated_tokens: 246, total_tokens: 1060, tokens_per_second: 61.4, queue_duration_ms: 18, load_duration_ms: 0, autoloaded: false },
  { id: 500, request_id: 'req_d4e5f6', trace_id: 'trace_fixture', session_id: 'session_fixture', session_total_count: 2, model_name: 'Gemma 3 12B', call_type: 'response', accepted_at: now - 54_000, started_at: now - 53_600, finished_at: now - 50_100, instance_id: 'gemma-always-on', endpoint: '/v1/responses', api_key: { id: 'key-ci', name: 'Evaluation', prefix: 'lcm_sk_cd34' }, streaming: false, status_code: 200, result: 'success', duration_ms: 3500, ttft_ms: 242, prompt_tokens: 1280, generated_tokens: 302, total_tokens: 1582, tokens_per_second: 48.1, queue_duration_ms: 31, load_duration_ms: 0, autoloaded: false }
]

const apiKeys = [
  { id: 'key-default', name: 'Open WebUI', prefix: 'lcm_sk_ab12', enabled: true, created_at: nowSeconds - 86400 * 21, last_used_at: nowSeconds - 18 },
  { id: 'key-ci', name: 'Evaluation', prefix: 'lcm_sk_cd34', enabled: false, created_at: nowSeconds - 86400 * 8, last_used_at: nowSeconds - 3600 },
  { id: 'key-revoked', name: 'Retired integration', prefix: 'lcm_sk_ef56', enabled: false, revoked_at: nowSeconds - 7200, created_at: nowSeconds - 86400 * 45, last_used_at: nowSeconds - 10800 }
]

const user = { id: 1, username: 'admin', enabled: true, created_at: nowSeconds - 86400 * 120, last_login_at: nowSeconds - 300 }
const profileSessions = [
  { id: 'session-current', user_id: 1, created_at: nowSeconds - 7200, expires_at: nowSeconds + 43200, remote_address: '192.0.2.24', user_agent: 'Mozilla/5.0 (Macintosh; Intel Mac OS X) AppleWebKit/537.36 Chrome/140.0.0.0 Safari/537.36', current: true },
  { id: 'session-other', user_id: 1, created_at: nowSeconds - 86400, expires_at: nowSeconds + 21600, remote_address: '198.51.100.18', user_agent: 'Mozilla/5.0 (X11; Linux x86_64) Gecko/20100101 Firefox/142.0' }
]
const profileIdentities = [{ id: 'identity-authentik', provider_id: 'authentik', issuer: 'https://auth.example.test/application/o/llamacpp-manager/', subject: 'admin-subject', user_id: 1, created_at: nowSeconds - 86400 * 30 }]
const systemLogs = [
  { timestamp: new Date(now - 90000).toISOString(), level: 'INFO', source: 'manager', message: 'reconcile: 2 Always On Instances satisfied' },
  { timestamp: new Date(now - 60000).toISOString(), level: 'WARN', source: 'gateway', message: 'request queue depth reached 4 while qwen3-primary was loading' },
  { timestamp: new Date(now - 30000).toISOString(), level: 'ERROR', source: 'qwen3-primary', message: 'worker exited unexpectedly after llama-server reported a representative long diagnostic message that verifies wrapping remains readable on narrow viewports' },
  { timestamp: new Date(now - 10000).toISOString(), level: 'DEBUG', source: 'telemetry', message: 'mapped host PID 1421 to managed Instance qwen3-primary' }
]
const setting = <T>(value: T, source = 'database', editable = true) => ({ value, source, editable })
const generalSettings = {
  session_lifetime_seconds: setting(86400),
  login_protection_enabled: setting(true),
  login_failure_threshold: setting(5),
  login_lockout_seconds: setting(900),
  trusted_proxies: setting('127.0.0.1/32'),
  allowed_origins: setting('http://127.0.0.1:3000'),
  external_url: setting('http://127.0.0.1:8888'),
  startup_timeout_seconds: setting(180),
  idle_unload_seconds: setting(900),
  always_on_reconcile_seconds: setting(15),
  observability_retention_days: setting(30),
  prometheus_auth_token: setting(''),
  runtime: {
    data_dir: '/var/lib/llamacpp-manager',
    models_dir: '/models',
    database_path: '/var/lib/llamacpp-manager/manager.db',
    listen_addr: ':8888',
    llama_server_path: '/usr/local/bin/llama-server'
  }
}
const authSettings = {
  local_login_enabled: setting(true),
  oidc_jit_provisioning_enabled: setting(true),
  oidc_auto_link_enabled: setting(false),
  external_url: setting('http://127.0.0.1:8888'),
  frontend_url: setting('http://127.0.0.1:3000')
}
const adminAuthProviders = [
  {
    id: 'authentik', name: 'Authentik', enabled: true, issuer: 'https://auth.example.test/application/o/llamacpp-manager/',
    client_id: 'llamacpp-manager', scopes: ['openid', 'profile', 'email'], username_claim: 'preferred_username',
    secret_configured: true, last_tested_at: nowSeconds - 3600, last_test_succeeded: true
  }
]
const llamaProfile = {
  path: '/usr/local/bin/llama-server', version: 'b6124', fingerprint: 'fixture-b6124',
  options: [
    { key: 'ctx-size', value_hint: 'N', description: 'Size of the prompt context', kind: 'number' },
    { key: 'parallel', value_hint: 'N', description: 'Number of parallel sequences', kind: 'number' },
    { key: 'flash-attn', description: 'Enable Flash Attention', kind: 'boolean' }
  ]
}
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
  if (pathname === '/api/v1/me/sessions') return profileSessions
  if (pathname === '/api/v1/me/identities') return profileIdentities
  if (pathname === '/api/v1/models' && method === 'GET') return models
  if (/^\/api\/v1\/models\/[^/]+$/.test(pathname) && method === 'GET') {
    return models.find(item => item.id === decodeURIComponent(pathname.split('/')[4] || '')) || {}
  }
  if (/^\/api\/v1\/models\/[^/]+\/options$/.test(pathname)) return {}
  if (/^\/api\/v1\/instances\/[^/]+$/.test(pathname) && method === 'GET') {
    return instances.find(item => item.id === decodeURIComponent(pathname.split('/')[4] || '')) || {}
  }
  if (/^\/api\/v1\/instances\/[^/]+\/options$/.test(pathname)) return {}
  if (pathname === '/api/v1/models/available') return [{
    path: '/models/Qwen3-Vision-8B-Q4_K_M.gguf', name: 'Qwen3 Vision 8B', total_bytes: 5_420_000_000,
    modified_at: new Date(now - 300_000).toISOString(), quantization: 'Q4_K_M',
    suggested_options: {
      mmproj: '/models/mmproj-Qwen3-Vision-F16.gguf',
      'spec-draft-model': '/models/Qwen3-0.6B-MTP-Q8_0.gguf',
      'spec-type': 'draft-mtp'
    }
  }]
  if (pathname === '/api/v1/models/inspect') return {
    id: 'local-qwen3-vision', name: 'Qwen3-Vision-8B-Q4_K_M.gguf', model_name: 'Qwen3 Vision 8B',
    architecture: 'qwen3', context_length: 32768, gguf_version: 3, metadata_count: 42,
    model_bytes: 5_420_000_000, total_bytes: 6_340_000_000, shard_count: 1, expected_shards: 1, complete: true,
    files: [{ path: '/models/Qwen3-Vision-8B-Q4_K_M.gguf', size: 5_420_000_000 }],
    suggested_options: {
      mmproj: '/models/mmproj-Qwen3-Vision-F16.gguf',
      'spec-draft-model': '/models/Qwen3-0.6B-MTP-Q8_0.gguf',
      'spec-type': 'draft-mtp'
    },
    dependencies: [
      { kind: 'mmproj', name: 'mmproj-Qwen3-Vision-F16.gguf', total_bytes: 620_000_000, files: [{ path: '/models/mmproj-Qwen3-Vision-F16.gguf', size: 620_000_000 }], option_path: '/models/mmproj-Qwen3-Vision-F16.gguf' },
      { kind: 'mtp', name: 'Qwen3-0.6B-MTP-Q8_0.gguf', quantization: 'Q8_0', total_bytes: 300_000_000, files: [{ path: '/models/Qwen3-0.6B-MTP-Q8_0.gguf', size: 300_000_000 }], option_path: '/models/Qwen3-0.6B-MTP-Q8_0.gguf' }
    ],
    dependency_candidates: [
      { kind: 'mmproj', name: 'mmproj-Qwen3-Vision-F16.gguf', total_bytes: 620_000_000, files: [{ path: '/models/mmproj-Qwen3-Vision-F16.gguf', size: 620_000_000 }], option_path: '/models/mmproj-Qwen3-Vision-F16.gguf' },
      { kind: 'mmproj', name: 'mmproj-Qwen3-Vision-Q8_0.gguf', quantization: 'Q8_0', total_bytes: 410_000_000, files: [{ path: '/models/mmproj-Qwen3-Vision-Q8_0.gguf', size: 410_000_000 }], option_path: '/models/mmproj-Qwen3-Vision-Q8_0.gguf' },
      { kind: 'mtp', name: 'Qwen3-0.6B-MTP-Q8_0.gguf', quantization: 'Q8_0', total_bytes: 300_000_000, files: [{ path: '/models/Qwen3-0.6B-MTP-Q8_0.gguf', size: 300_000_000 }], option_path: '/models/Qwen3-0.6B-MTP-Q8_0.gguf' },
      { kind: 'mtp', name: 'Qwen3-0.6B-MTP-Q4_K_M.gguf', quantization: 'Q4_K_M', total_bytes: 185_000_000, files: [{ path: '/models/Qwen3-0.6B-MTP-Q4_K_M.gguf', size: 185_000_000 }], option_path: '/models/Qwen3-0.6B-MTP-Q4_K_M.gguf' }
    ]
  }
  if (pathname === '/api/v1/models/qwen3-8b-q4km/details') return { model: models[0], gguf_version: 3, tensor_count: 291, metadata_count: 12, metadata_total: 12, metadata: [{ key: 'general.architecture', type: 'string', value: 'qwen3' }, { key: 'general.name', type: 'string', value: 'Qwen3 8B Instruct' }, { key: 'general.quantization_version', type: 'uint32', value: '2' }, { key: 'qwen3.context_length', type: 'uint32', value: '32768' }, { key: 'qwen3.embedding_length', type: 'uint32', value: '4096' }, { key: 'qwen3.block_count', type: 'uint32', value: '36' }, { key: 'qwen3.attention.head_count', type: 'uint32', value: '32' }, { key: 'qwen3.attention.head_count_kv', type: 'uint32', value: '8' }, { key: 'tokenizer.ggml.model', type: 'string', value: 'gpt2' }, { key: 'tokenizer.ggml.pre', type: 'string', value: 'qwen2' }, { key: 'tokenizer.ggml.tokens', type: 'array[string]', value: '[151936 items]', truncated: true, array_length: 151936 }, { key: 'tokenizer.chat_template', type: 'string', value: '{% for message in messages %} ... representative long template ... {% endfor %}', truncated: true }], architecture: 'qwen3', detected_context_length: 32768, offset: 0, limit: 100, warnings: ['Representative fixture warning: tokenizer metadata contains a truncated value.'] }
  if (pathname === '/api/v1/models/qwen3-8b-q4km/details/value') return { key: 'tokenizer.ggml.tokens', type: 'array[string]', items: ['<|endoftext|>', '<|im_start|>', '<|im_end|>', 'hello'], offset: 0, limit: 100, total: 4, has_more: false }
  if (pathname === '/api/v1/instances' && method === 'GET') return instances.slice(0, 2)
  if (pathname === '/api/v1/imports') return []
  if (/^\/api\/v1\/instances\/[^/]+\/runtime$/.test(pathname)) return runtimes[decodeURIComponent(pathname.split('/')[4] || '')] || { state: 'UNLOADED' }
  if (pathname === '/api/v1/auth/ws-ticket') return { error: 'disabled in screenshot fixture' }
  if (pathname === '/api/v1/llamacpp/profile') return { available: true, profile: llamaProfile }
  if (pathname === '/api/v1/llamacpp/config') return { profile: llamaProfile, effective: { global: {}, values: {}, sources: {} } }
  if (pathname === '/api/v1/settings/general') return generalSettings
  if (pathname === '/api/v1/settings/discover') return { hybrid_recommendations_enabled: setting(true) }
  if (pathname === '/api/v1/admin/auth/settings') return authSettings
  if (pathname === '/api/v1/admin/auth/providers') return adminAuthProviders
  if (pathname === '/api/v1/admin/summary') return { users: { total: 2, enabled: 2 }, huggingface: { configured: true, prefix: 'hf_demo' }, llamacpp: { available: true, path: llamaProfile.path, version: llamaProfile.version, fingerprint: llamaProfile.fingerprint } }
  if (pathname === '/api/v1/observability/summary') return {
    since: now - 900_000, requests: 1842, successes: 1829, errors: 13, active: 3, queued: 1, active_api_keys: 2,
    prompt_tokens: 1_488_420, generated_tokens: 624_980, total_tokens: 2_113_400,
    lifecycle: { autoloads: 7, failed_starts: 1, load_duration_ms_total: 38_400 },
    hardware: { hardware, telemetry }
  }
  if (pathname === '/api/v1/observability/requests') return { items: requests, next_cursor: '' }
  if (pathname === '/api/v1/observability/playground/req_playground_fixture') return { request: { request_id: 'req_playground_fixture', instance_id: 'qwen3-primary', status_code: 200, result: 'success', duration_ms: 1680, ttft_ms: 182, prompt_tokens: 42, generated_tokens: 96, total_tokens: 138, tokens_per_second: 57.1, load_duration_ms: 0, autoloaded: false }, state_trace: ['READY', 'GENERATING', 'READY'], evictions_triggered: [] }
  if (pathname === '/api/v1/observability/timeseries') return { metric: 'fixture', bucket_seconds: 60, items: [{ timestamp: now - 120_000, value: 18 }, { timestamp: now - 60_000, value: 31 }, { timestamp: now, value: 24 }] }
  if (pathname === '/api/v1/hardware' || pathname === '/api/v1/hardware/snapshot') return hardware
  if (pathname === '/api/v1/api-keys' && method === 'GET') return apiKeys
  if (pathname === '/api/v1/api-keys' && method === 'POST') return { key: { id: 'key-visual', name: 'Visual QA', prefix: 'lcm_sk_vis', enabled: true, created_at: nowSeconds, last_used_at: 0 }, secret: 'lcm_sk_visual_fixture_secret' }
  if (pathname === '/api/v1/system') return {
    manager: { uptime_seconds: 104_822, runtime: { go_version: 'go1.25.0', os: 'linux', arch: 'amd64' } },
    network: { effective_scheme: 'http', secure_cookie: false, allowed_origins: 'http://127.0.0.1:3000', trusted_proxies: '127.0.0.1/32', external_url: 'http://127.0.0.1:8888' },
    llamacpp: { available: true, path: '/usr/local/bin/llama-server', version: 'b6124', fingerprint: 'fixture-b6124', options: 146 }
  }
  if (/\/users(?:\/|$)/.test(pathname)) return [user, { id: 2, username: 'operator', enabled: true }]
  if (/\/api-keys(?:\/|$)/.test(pathname)) return apiKeys
  if (pathname === '/api/v1/downloads' && method === 'GET') return [
    {
      id: 'dl-active', provider: 'huggingface', repo_id: 'Qwen/Qwen3-8B-GGUF', revision: 'main', artifact_id: 'q4_k_m',
      name: 'Qwen3 8B Q4_K_M', quantization: 'Q4_K_M', state: 'DOWNLOADING', total_bytes: 5420000000, downloaded_bytes: 2168000000,
      speed_bps: 84000000, created_at: nowSeconds - 240, updated_at: nowSeconds,
      files: [
        { path: 'Qwen3-8B-Q4_K_M-00001-of-00002.gguf', size: 2710000000, state: 'COMPLETED', downloaded_bytes: 2710000000 },
        { path: 'Qwen3-8B-Q4_K_M-00002-of-00002.gguf', size: 2710000000, state: 'DOWNLOADING', downloaded_bytes: 1610000000 }
      ]
    },
    {
      id: 'dl-verifying', provider: 'huggingface', repo_id: 'mistralai/Mistral-Small-GGUF', revision: 'main', artifact_id: 'q4',
      name: 'Mistral Small Q4_K_M', quantization: 'Q4_K_M', state: 'VERIFYING', total_bytes: 12900000000, downloaded_bytes: 12900000000,
      speed_bps: 0, created_at: nowSeconds - 1500, updated_at: nowSeconds - 20,
      files: [{ path: 'Mistral-Small-Q4_K_M.gguf', size: 12900000000, state: 'VERIFYING', downloaded_bytes: 12900000000 }]
    },
    {
      id: 'dl-failed', provider: 'huggingface', repo_id: 'example/failed-model', revision: 'main', artifact_id: 'q5',
      name: 'Failed model import', quantization: 'Q5_K_M', state: 'FAILED', total_bytes: 7200000000, downloaded_bytes: 1100000000,
      speed_bps: 0, error: 'Connection reset while downloading shard 2 of 3. Retry resumes verified partial files.',
      created_at: nowSeconds - 900, updated_at: nowSeconds - 300
    },
    {
      id: 'dl-cancelled', provider: 'huggingface', repo_id: 'example/cancelled-model', revision: 'main', artifact_id: 'q6',
      name: 'Cancelled model import', quantization: 'Q6_K', state: 'CANCELLED', total_bytes: 9300000000, downloaded_bytes: 3400000000,
      speed_bps: 0, created_at: nowSeconds - 1800, updated_at: nowSeconds - 600,
      files: [{ path: 'cancelled-model-Q6_K.gguf', size: 9300000000, state: 'CANCELLED', downloaded_bytes: 3400000000 }]
    },
    {
      id: 'dl-completed', provider: 'huggingface', repo_id: 'google/gemma-3-12b-it-GGUF', revision: 'main', artifact_id: 'q5_k_m',
      name: 'Gemma 3 12B Q5_K_M', quantization: 'Q5_K_M', state: 'COMPLETED', total_bytes: 8230000000, downloaded_bytes: 8230000000,
      speed_bps: 0, created_at: nowSeconds - 7200, updated_at: nowSeconds - 3600
    }
  ]
  if (/\/observability\/requests\//.test(pathname)) return requests[0]
  if (pathname === '/api/v1/logs') return { entries: systemLogs }
  if (/\/logs(?:\/|$)/.test(pathname)) return { items: [] }
  if (pathname === '/api/v1/huggingface/token') return { configured: true, prefix: 'hf_demo' }
  if (pathname === '/api/v1/huggingface/search') return { items: [{ id: 'Qwen/Qwen3-8B-GGUF', author: 'Qwen', downloads: 420000, likes: 980, last_modified: new Date(now - 3600000).toISOString(), parameter_count: 8000000000, tags: ['gguf', 'text-generation'], private: false, gated: false }], next_cursor: '' }
  if (pathname === '/api/v1/huggingface/model') return { id: 'Qwen/Qwen3-8B-GGUF', author: 'Qwen', downloads: 420000, likes: 980, last_modified: new Date(now - 3600000).toISOString(), parameter_count: 8000000000, tags: ['gguf', 'text-generation'], private: false, gated: false, description: 'Representative Qwen3 GGUF repository used for deterministic visual QA.', revision: 'main', artifacts: [{ id: 'q4_k_m', name: 'Qwen3-8B-Q4_K_M.gguf', quantization: 'Q4_K_M', model_bytes: 5420000000, total_bytes: 5420000000, shard_count: 1, expected_shards: 1, complete: true, files: [{ path: 'Qwen3-8B-Q4_K_M.gguf', size: 5420000000 }] }, { id: 'q8_0', name: 'Qwen3-8B-Q8_0.gguf', quantization: 'Q8_0', model_bytes: 8600000000, total_bytes: 8600000000, shard_count: 1, expected_shards: 1, complete: true, files: [{ path: 'Qwen3-8B-Q8_0.gguf', size: 8600000000 }] }] }
  if (pathname === '/api/v1/huggingface/recommendations') return { context_length: 32768, context_capability: 32768, context_assumed: false, metadata: { architecture: 'qwen3', context_length: 32768, block_count: 36, embedding_length: 4096, head_count: 32, kv_head_count: 8 }, hardware_available: true, hybrid_recommendations_enabled: true, artifacts: [{ artifact_id: 'q4_k_m', quantization: { name: 'Q4_K_M', tier: 'balanced', quality: 'Good', memory: 'Low', speed: 'Fast', summary: 'Recommended balance', tradeoff: 'Some quality loss', known: true }, recommended: true, runnable: true, fit: 'gpu', fit_label: 'Fits one GPU', reason: 'Fits on cuda:0 with context headroom.', memory: { weights_bytes: 5420000000, kv_cache_bytes: 1200000000, runtime_overhead_bytes: 600000000, cpu_only_ram_bytes: 7600000000, full_offload_vram_bytes: 7220000000 }, offload: { mode: 'full_gpu', devices: ['cuda:0'], kv_on_gpu: true, reason: 'Single-GPU full offload fits.' }, estimated_generation_speed: { estimated: true, min_tokens_per_second: 45, max_tokens_per_second: 68, label: '45–68 tok/s', reason: 'Estimated from available memory bandwidth.' }, confidence: 'high', warnings: [] }, { artifact_id: 'q8_0', quantization: { name: 'Q8_0', tier: 'quality', quality: 'Very high', memory: 'High', speed: 'Moderate', summary: 'Higher quality', tradeoff: 'Higher VRAM use', known: true }, recommended: false, runnable: true, fit: 'multi_gpu', fit_label: 'Fits across two GPUs', reason: 'Requires both configured GPUs for comfortable headroom.', memory: { weights_bytes: 8600000000, kv_cache_bytes: 1200000000, runtime_overhead_bytes: 700000000, cpu_only_ram_bytes: 11000000000, full_offload_vram_bytes: 10500000000 }, offload: { mode: 'multi_gpu', devices: ['cuda:0', 'cuda:1'], tensor_split: '0.5,0.5', kv_on_gpu: true, reason: 'Split weights across two GPUs.' }, confidence: 'medium', warnings: ['Uses more VRAM than the recommended artifact.'] }] }
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
    if (modelInspectFailurePages.has(page) && url.pathname === '/api/v1/models/inspect' && request.method() === 'POST') {
      await route.fulfill({ status: 422, headers: corsHeaders, body: JSON.stringify({ error: 'Representative GGUF metadata inspection failure for visual QA.' }) })
      return
    }
    if (instancesStatePages.has(page) && url.pathname === '/api/v1/instances' && request.method() === 'GET') {
      await route.fulfill({ status: 200, headers: corsHeaders, body: JSON.stringify(instances) })
      return
    }
    if (url.pathname === '/api/v1/models/qwen3-8b-q4km/details') {
      if (modelDetailsFailurePages.has(page)) {
        await route.fulfill({ status: 503, headers: corsHeaders, body: JSON.stringify({ error: 'Representative GGUF details failure for visual QA.' }) })
        return
      }
      const base = responseFor(url.pathname, request.method()) as Record<string, any>
      const filter = url.searchParams.get('q') || ''
      if (filter) {
        await route.fulfill({ status: 200, headers: corsHeaders, body: JSON.stringify({ ...base, metadata: [], metadata_total: 0, offset: 0 }) })
        return
      }
      if (modelDetailsPageTwoPages.has(page)) {
        const pageOffset = Number(url.searchParams.get('offset') || 0)
        const metadata = pageOffset >= 100
          ? [
              { key: 'metadata.page2.first', type: 'string', value: 'First key on the second page' },
              { key: 'metadata.page2.second', type: 'uint32', value: '42' },
              { key: 'metadata.page2.third', type: 'string', value: 'Last representative key' }
            ]
          : Array.from({ length: 100 }, (_, index) => ({ key: `metadata.page1.key_${String(index + 1).padStart(3, '0')}`, type: 'string', value: `Representative metadata value ${index + 1}` }))
        await route.fulfill({ status: 200, headers: corsHeaders, body: JSON.stringify({ ...base, metadata_count: 103, metadata_total: 103, metadata, offset: pageOffset, limit: 100 }) })
        return
      }
    }
    if (instancesStatePages.has(page) && url.pathname === '/api/v1/imports') {
      await route.fulfill({ status: 200, headers: corsHeaders, body: JSON.stringify([{ id: 'import-qwen3-downloading', job_id: 'dl-qwen3-downloading', model_id: 'qwen3-8b-q4km', instance_id: 'qwen3-downloading', state: 'DOWNLOADING', start_when_ready: true }]) })
      return
    }
    if (dashboardFailurePages.has(page) && /^\/api\/v1\/instances\/[^/]+\/runtime$/.test(url.pathname)) {
      const instanceID = decodeURIComponent(url.pathname.split('/')[4] || '')
      const runtime = instanceID === 'qwen3-primary'
        ? { instance_id: 'qwen3-primary', model_id: 'qwen3-8b-q4km', state: 'FAILED', last_error: 'CUDA out of memory while allocating the KV cache.' }
        : responseFor(url.pathname, request.method())
      await route.fulfill({ status: 200, headers: corsHeaders, body: JSON.stringify(runtime) })
      return
    }
    if (dashboardFailurePages.has(page) && url.pathname === '/api/v1/observability/summary') {
      await route.fulfill({ status: 200, headers: corsHeaders, body: JSON.stringify({
        since: now - 900_000, requests: 12, successes: 10, errors: 2, active: 0, queued: 0, active_api_keys: 2,
        prompt_tokens: 14_220, generated_tokens: 6_480, total_tokens: 20_700,
        lifecycle: { autoloads: 3, failed_starts: 1, load_duration_ms_total: 19_200 },
        hardware: { hardware, telemetry }
      }) })
      return
    }
    if (dashboardFailurePages.has(page) && url.pathname === '/api/v1/observability/requests') {
      await route.fulfill({ status: 200, headers: corsHeaders, body: JSON.stringify({ items: [{
        id: 599, request_id: 'req_dashboard_failure', trace_id: 'trace_dashboard_failure', session_id: 'session_dashboard_failure',
        accepted_at: now - 9_000, started_at: now - 8_900, finished_at: now - 8_100,
        instance_id: 'qwen3-primary', endpoint: '/v1/chat/completions', api_key: { id: 'key-default', name: 'Open WebUI', prefix: 'lcm_sk_ab12' },
        streaming: true, status_code: 503, result: 'error', duration_ms: 800, ttft_ms: 0,
        prompt_tokens: 128, generated_tokens: 0, total_tokens: 128, tokens_per_second: 0,
        queue_duration_ms: 22, load_duration_ms: 0, autoloaded: false,
        error: 'CUDA out of memory while allocating the KV cache.'
      }], next_cursor: '' }) })
      return
    }
    if (discoverDetailFailurePages.has(page) && url.pathname === '/api/v1/huggingface/model') {
      await route.fulfill({ status: 503, headers: corsHeaders, body: JSON.stringify({ error: 'Repository temporarily unavailable for visual QA.' }) })
      return
    }
    if (playgroundColdPages.has(page) && /^\/api\/v1\/instances\/[^/]+\/runtime$/.test(url.pathname)) {
      await route.fulfill({ status: 200, headers: corsHeaders, body: JSON.stringify({ instance_id: 'qwen3-primary', model_id: 'qwen3-8b-q4km', state: 'UNLOADED' }) })
      return
    }
    if (url.pathname === '/api/v1/playground/chat/completions' && request.method() === 'POST') {
      const payload = request.postData() || ''
      if (payload.includes('UX_ERROR')) {
        await route.fulfill({ status: 503, headers: corsHeaders, body: JSON.stringify({ error: { message: 'Representative gateway failure for visual QA.' } }) })
        return
      }
      if (payload.includes('UX_HOLD')) await new Promise<void>(resolve => playgroundHoldRelease.set(page, resolve))
      await route.fulfill({
        status: 200,
        headers: { ...corsHeaders, 'access-control-expose-headers': 'x-llamacpp-manager-request-id', 'content-type': 'text/event-stream', 'x-llamacpp-manager-request-id': 'req_playground_fixture' },
        body: ['data: {"choices":[{"delta":{"content":"KV cache reuse "}}]}', 'data: {"choices":[{"delta":{"content":"avoids repeated prompt evaluation."}}]}', 'data: [DONE]'].join('\n\n') + '\n\n'
      })
      return
    }
    if (url.pathname === '/api/v1/auth/ws-ticket') {
      await route.fulfill({ status: 503, headers: corsHeaders, body: JSON.stringify(responseFor(url.pathname, request.method())) })
      return
    }
    await route.fulfill({ status: 200, headers: corsHeaders, body: JSON.stringify(responseFor(url.pathname, request.method())) })
  })
}

async function waitForManagerPanel(page: Page) {
  const panel = page.locator('#dashboard-panel-manager-main')
  await expect(panel).toBeVisible({ timeout: 15_000 })
  await expect(panel).not.toBeEmpty()
  await expect(page.getByRole('heading', { name: 'Manager connection failed' })).toBeHidden()
  await expect(page.getByRole('heading', { name: 'Welcome back' })).toBeHidden()
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
  ['request-log-detail', '/logs?request_id=req_a1b2c3&session_id=session_fixture'],
  ['request-logs-trace', '/logs?trace_id=trace_fixture'],
  ['api-keys', '/api'],
  ['profile-account', '/profile/account'],
  ['profile-authentication', '/profile/authentication'],
  ['profile-sessions', '/profile/sessions'],
  ['admin-overview', '/admin'],
  ['admin-authentication', '/admin/authentication'],
  ['admin-general', '/admin/general'],
  ['admin-huggingface', '/admin/huggingface'],
  ['admin-llamacpp', '/admin/llamacpp'],
  ['admin-logs', '/admin/system-logs'],
  ['admin-system', '/admin/system'],
  ['admin-users', '/admin/users']
] as const

test.beforeEach(async ({ page }) => {
  await installApiFixture(page)
  await page.emulateMedia({ reducedMotion: 'reduce' })
})

for (const [name, path] of pages) {
  test(`${name} screenshot`, async ({ page }, testInfo) => {
    if (name === 'admin-logs') await page.addInitScript(() => { Object.defineProperty(window, 'EventSource', { value: undefined, configurable: true }) })
    await page.goto(path, { waitUntil: 'domcontentloaded' })
    await waitForManagerPanel(page)
    if (name === 'model-new') {
      const option = page.locator('[data-testid="gguf-option"]').first()
      await expect(option).toBeVisible()
      await option.click()
      await expect(page.locator('[data-testid="model-name"]')).toHaveValue('Qwen3 Vision 8B')
      await expect(page.locator('[data-testid^="companion-candidate-"]')).toHaveCount(4)
    }
    if (name === 'profile-account') {
      await expect(page.locator('[data-testid="profile-account"]')).toBeVisible()
      await expect(page.locator('[data-testid="profile-password"]')).toBeVisible()
    }
    if (name === 'profile-authentication') {
      await expect(page.locator('[data-testid="profile-authentication-sources"]')).toContainText('Authentik')
    }
    if (name === 'profile-sessions') {
      await expect(page.locator('[data-testid="profile-sessions"]')).toContainText('Current')
    }
    if (name === 'admin-logs') await expect(page.locator('[data-testid="system-log-row"]')).toHaveCount(4)
    const documentOverflow = await page.evaluate(() => Math.max(document.documentElement.scrollWidth, document.body.scrollWidth) - window.innerWidth)
    expect(documentOverflow, `${name} document should not overflow horizontally`).toBeLessThanOrEqual(1)
    if (name !== 'model-new') await page.waitForTimeout(800)
    await page.screenshot({
      path: `artifacts/ux-screenshots/${testInfo.project.name}/${name}.png`,
      fullPage: true,
      animations: 'disabled'
    })
  })
}



test('model details filtered no-match screenshot', async ({ page }, testInfo) => {
  await page.goto('/models/qwen3-8b-q4km/details', { waitUntil: 'domcontentloaded' })
  await waitForManagerPanel(page)
  await expect(page.getByRole('heading', { name: 'Qwen3 8B' })).toBeVisible()
  await page.locator('[data-testid="metadata-search"]').fill('tokenizer.missing')
  await page.locator('[data-testid="metadata-search-button"]').click()
  await expect(page.getByText('No matching GGUF metadata', { exact: true })).toBeVisible()
  await expect(page.getByText('No keys match “tokenizer.missing”. Clear the filter to see all metadata.', { exact: true })).toBeVisible()
  await expect(page.getByText('No matching keys', { exact: true })).toBeVisible()
  await page.screenshot({ path: `artifacts/ux-screenshots/${testInfo.project.name}/model-details-no-match.png`, fullPage: true, animations: 'disabled' })
})


test('model details second page screenshot', async ({ page }, testInfo) => {
  modelDetailsPageTwoPages.add(page)
  await page.goto('/models/qwen3-8b-q4km/details', { waitUntil: 'domcontentloaded' })
  await waitForManagerPanel(page)
  await expect(page.getByText('Showing 1–100 of 103 matching keys', { exact: true })).toBeVisible()
  await page.getByRole('button', { name: 'Next', exact: true }).click()
  await expect(page.getByText('Showing 101–103 of 103 matching keys', { exact: true })).toBeVisible()
  await expect(page.getByText('metadata.page2.first', { exact: true })).toBeVisible()
  await page.screenshot({ path: `artifacts/ux-screenshots/${testInfo.project.name}/model-details-page-2.png`, fullPage: true, animations: 'disabled' })
})


test('model details API failure screenshot', async ({ page }, testInfo) => {
  modelDetailsFailurePages.add(page)
  await page.goto('/models/qwen3-8b-q4km/details', { waitUntil: 'domcontentloaded' })
  await waitForManagerPanel(page)
  const failure = page.locator('[data-testid="model-details-error"]')
  await expect(failure).toContainText('Unable to load GGUF metadata')
  await expect(failure).toContainText('Representative GGUF details failure for visual QA.')
  await page.screenshot({ path: `artifacts/ux-screenshots/${testInfo.project.name}/model-details-api-failure.png`, fullPage: true, animations: 'disabled' })
})

test('model details expanded metadata screenshot', async ({ page }, testInfo) => {
  await page.goto('/models/qwen3-8b-q4km/details', { waitUntil: 'domcontentloaded' })
  await waitForManagerPanel(page)
  await expect(page.getByRole('heading', { name: 'Qwen3 8B' })).toBeVisible()
  await page.locator('[data-testid="metadata-expand"]').first().click()
  await expect(page.locator('[data-testid="metadata-expanded-items"]')).toContainText('<|endoftext|>')
  await page.screenshot({ path: `artifacts/ux-screenshots/${testInfo.project.name}/model-details-expanded.png`, fullPage: true, animations: 'disabled' })
})



test('model-new metadata inspection failure screenshot', async ({ page }, testInfo) => {
  modelInspectFailurePages.add(page)
  await page.goto('/models/new', { waitUntil: 'domcontentloaded' })
  await waitForManagerPanel(page)
  const option = page.locator('[data-testid="gguf-option"]').first()
  await expect(option).toBeVisible()
  await option.click()
  await expect(page.locator('[data-testid="metadata-warning"]')).toContainText('Representative GGUF metadata inspection failure for visual QA.')
  await page.screenshot({ path: `artifacts/ux-screenshots/${testInfo.project.name}/model-new-metadata-failure.png`, fullPage: true, animations: 'disabled' })
})


test('model-new Hugging Face remote screenshot', async ({ page }, testInfo) => {
  await page.goto('/models/new?repo=Qwen%2FQwen3-8B-GGUF&artifact=q4_k_m', { waitUntil: 'domcontentloaded' })
  await waitForManagerPanel(page)
  const artifact = page.locator('[data-testid="remote-artifact-summary"]')
  await expect(artifact).toContainText('Qwen/Qwen3-8B-GGUF')
  await expect(artifact).toContainText('Qwen3-8B-Q4_K_M.gguf')
  await expect(page.locator('[data-testid="model-name"]')).toHaveValue('Qwen3-8B-GGUF Q4_K_M')
  await expect(page.locator('[data-testid="model-form-first-instance"]')).toBeVisible()
  await page.screenshot({ path: `artifacts/ux-screenshots/${testInfo.project.name}/model-new-remote.png`, fullPage: true, animations: 'disabled' })
})

test('discover repository failure and retry screenshot', async ({ page }, testInfo) => {
  discoverDetailFailurePages.add(page)
  await page.goto('/models/discover/Qwen/Qwen3-8B-GGUF', { waitUntil: 'domcontentloaded' })
  await waitForManagerPanel(page)
  const failure = page.locator('[data-testid="discover-detail-error"]')
  await expect(failure).toContainText('Repository temporarily unavailable for visual QA.')
  await expect(failure).toContainText('Qwen/Qwen3-8B-GGUF')
  await expect(page.locator('[data-testid="discover-detail-back"]')).toBeVisible()
  await expect(page.locator('[data-testid="discover-detail-retry"]')).toBeVisible()
  await page.screenshot({ path: `artifacts/ux-screenshots/${testInfo.project.name}/discover-detail-error.png`, fullPage: true, animations: 'disabled' })

  discoverDetailFailurePages.delete(page)
  await page.locator('[data-testid="discover-detail-retry"]').click()
  await expect(page.locator('[data-testid="discover-repository-header"]').getByRole('heading', { name: 'Qwen/Qwen3-8B-GGUF' })).toBeVisible()
  await expect(page.locator('[data-testid="discover-detail-error"]')).toBeHidden()
})


test('downloads lifecycle and files screenshot', async ({ page }, testInfo) => {
  await page.goto('/downloads', { waitUntil: 'domcontentloaded' })
  await waitForManagerPanel(page)
  const queue = page.locator('[data-testid="download-queue"]')
  await expect(queue).toContainText('DOWNLOADING')
  await expect(queue).toContainText('VERIFYING')
  await expect(queue).toContainText('FAILED')
  await expect(queue).toContainText('CANCELLED')
  await page.locator('[data-testid="toggle-completed-downloads"]').click()
  await expect(queue).toContainText('COMPLETED')
  await page.getByRole('button', { name: /2 files/ }).first().click()
  await expect(page.locator('[data-testid="download-files"]').first()).toContainText('00002-of-00002.gguf')
  await page.screenshot({ path: `artifacts/ux-screenshots/${testInfo.project.name}/downloads-lifecycle.png`, fullPage: true, animations: 'disabled' })
})


test('instances operational card states screenshot', async ({ page }, testInfo) => {
  instancesStatePages.add(page)
  await page.goto('/instances', { waitUntil: 'domcontentloaded' })
  await waitForManagerPanel(page)
  await page.locator('[data-testid="instances-view-cards"]').click()
  const cards = page.locator('[data-testid="instances-card-view"]')
  await expect(cards).toContainText('READY')
  await expect(cards).toContainText('FAILED')
  await expect(cards).toContainText('UNLOADED')
  await expect(cards).toContainText('DOWNLOADING')
  await expect(cards).toContainText('CUDA out of memory')
  await expect(cards).toContainText('launch automatically when the verified GGUF download completes')
  await page.screenshot({ path: `artifacts/ux-screenshots/${testInfo.project.name}/instances-card-states.png`, fullPage: true, animations: 'disabled' })
})


test('dashboard failure recovery screenshot', async ({ page }, testInfo) => {
  dashboardFailurePages.add(page)
  await page.goto('/', { waitUntil: 'domcontentloaded' })
  await waitForManagerPanel(page)
  await expect(page.locator('[data-testid="dashboard-attention-link"]')).toContainText('Needs attention · 2')
  const attention = page.locator('[data-testid="dashboard-attention"]')
  await expect(attention).toContainText('qwen3-primary failed to start')
  await expect(attention).toContainText('qwen3-primary returned 503')
  await expect(attention.getByRole('link', { name: 'Review' })).toHaveCount(2)
  await page.screenshot({ path: `artifacts/ux-screenshots/${testInfo.project.name}/dashboard-failure-recovery.png`, fullPage: true, animations: 'disabled' })
})


test('playground generating and completed screenshots', async ({ page }, testInfo) => {
  await page.goto('/playground', { waitUntil: 'domcontentloaded' })
  await waitForManagerPanel(page)
  await page.getByLabel('Playground message').fill('UX_HOLD Explain KV cache reuse.')
  await page.getByRole('button', { name: 'Send', exact: true }).click()
  await expect(page.getByRole('button', { name: 'Stop', exact: true })).toBeVisible()
  await expect(page.getByText('Generating', { exact: true })).toBeVisible()
  await expect.poll(() => playgroundHoldRelease.has(page)).toBe(true)
  await page.screenshot({ path: `artifacts/ux-screenshots/${testInfo.project.name}/playground-generating.png`, fullPage: true, animations: 'disabled' })

  playgroundHoldRelease.get(page)?.()
  await expect(page.getByText('Completed', { exact: true })).toBeVisible()
  await expect(page.locator('[data-testid="playground-diagnostics"]')).toContainText('57.10 tok/s')
  await expect(page.locator('[data-testid="playground-thread"]')).toContainText('avoids repeated prompt evaluation.')
  await page.screenshot({ path: `artifacts/ux-screenshots/${testInfo.project.name}/playground-completed.png`, fullPage: true, animations: 'disabled' })

  await page.getByRole('button', { name: 'Request', exact: true }).click()
  await expect(page.getByLabel('Raw request JSON')).toHaveValue(/UX_HOLD Explain KV cache reuse\./)
  await page.screenshot({ path: `artifacts/ux-screenshots/${testInfo.project.name}/playground-request.png`, fullPage: true, animations: 'disabled' })
})

test('playground cold-start screenshot', async ({ page }, testInfo) => {
  playgroundColdPages.add(page)
  await page.goto('/playground', { waitUntil: 'domcontentloaded' })
  await waitForManagerPanel(page)
  await expect(page.getByText('UNLOADED', { exact: true }).first()).toBeVisible()
  await page.getByLabel('Playground message').fill('UX_HOLD Trigger autoload.')
  await page.getByRole('button', { name: 'Send', exact: true }).click()
  await expect(page.getByRole('button', { name: 'Stop', exact: true })).toBeVisible()
  await expect(page.getByText('Cold start — autoload in progress', { exact: true })).toBeVisible()
  await expect.poll(() => playgroundHoldRelease.has(page)).toBe(true)
  await page.screenshot({ path: `artifacts/ux-screenshots/${testInfo.project.name}/playground-cold-start.png`, fullPage: true, animations: 'disabled' })
  playgroundHoldRelease.get(page)?.()
  await expect(page.getByText('Completed', { exact: true })).toBeVisible()
})

test('playground error screenshot', async ({ page }, testInfo) => {
  await page.goto('/playground', { waitUntil: 'domcontentloaded' })
  await waitForManagerPanel(page)
  await page.getByLabel('Playground message').fill('UX_ERROR Trigger representative failure.')
  await page.getByRole('button', { name: 'Send', exact: true }).click()
  await expect(page.locator('[data-testid="playground-error"]')).toContainText('Representative gateway failure for visual QA.')
  await expect(page.getByText('Failed', { exact: true })).toBeVisible()
  await expect(page.getByText('Completed', { exact: true })).toBeHidden()
  await page.screenshot({ path: `artifacts/ux-screenshots/${testInfo.project.name}/playground-error.png`, fullPage: true, animations: 'disabled' })
})

test('api key secret and revoke confirmation screenshots', async ({ page }, testInfo) => {
  await page.goto('/api', { waitUntil: 'domcontentloaded' })
  await waitForManagerPanel(page)
  await expect(page.locator('[data-testid="api-keys-card"]')).toContainText('Retired integration')
  await expect(page.locator('[data-testid="api-keys-card"]')).toContainText('Revoked')
  await page.locator('[data-testid="key-name"]').fill('Visual QA')
  await page.locator('[data-testid="create-key"]').click()
  await expect(page.locator('[data-testid="fresh-api-key"]')).toContainText('lcm_sk_visual_fixture_secret')
  await page.screenshot({ path: `artifacts/ux-screenshots/${testInfo.project.name}/api-key-fresh-secret.png`, fullPage: true, animations: 'disabled' })

  await page.getByRole('button', { name: 'Revoke', exact: true }).first().click()
  await expect(page.locator('[data-testid="confirmation-confirm"]')).toContainText('Revoke key')
  await page.screenshot({ path: `artifacts/ux-screenshots/${testInfo.project.name}/api-key-revoke-confirmation.png`, fullPage: true, animations: 'disabled' })
})
