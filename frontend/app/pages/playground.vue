<script setup lang="ts">
import type { Instance, Model, RuntimeTelemetry } from '~/composables/useManager'
import { readManagementToken } from '~/composables/useManagerApi'

type Role = 'system' | 'user' | 'assistant'
type MessageStats = { prompt: number; completion: number; rate?: number; ttft?: number }
type ThreadMessage = { role: Role; content: string; stats?: MessageStats }
type RequestRecord = {
  request_id?: string
  instance_id: string | null
  status_code: number
  result: string
  duration_ms: number
  ttft_ms?: number
  prompt_tokens: number
  generated_tokens: number
  total_tokens: number
  tokens_per_second?: number
  load_duration_ms: number
  autoloaded: boolean
  error?: string
}
type PlaygroundDiagnostics = {
  request: RequestRecord
  state_trace: string[] | null
  evictions_triggered: string[] | null
}
type Parameters = {
  temperature: number
  topP: number
  maxTokens: number
  seed: string
  topK: number
  minP: number
  repeatPenalty: number
  stop: string
  stream: boolean
  systemPrompt: string
}

const manager = useManager()
const route = useRoute()
const selectedInstanceID = ref('')
const activePanel = ref<'parameters' | 'request' | 'response'>('parameters')
const composer = ref('')
const conversation = ref<ThreadMessage[]>([])
const rawRequest = ref('')
const rawDirty = ref(false)
const rawResponse = ref('')
const responseHeaders = ref<Array<[string, string]>>([])
const diagnostics = ref<PlaygroundDiagnostics | null>(null)
const error = ref('')
const notice = ref('')
const inFlight = ref(false)
const phase = ref<'cold' | 'generating' | 'completed' | 'failed' | ''>('')
const mobileParametersOpen = ref(false)
let controller: AbortController | null = null

const parameters = reactive<Parameters>({
  temperature: 0.7,
  topP: 0.95,
  maxTokens: 512,
  seed: '',
  topK: 40,
  minP: 0.05,
  repeatPenalty: 1.1,
  stop: '',
  stream: true,
  systemPrompt: ''
})

const selectedInstance = computed<Instance | undefined>(() => manager.instances.value.find(item => item.id === selectedInstanceID.value))
const selectedModel = computed<Model | undefined>(() => manager.models.value.find(item => item.id === selectedInstance.value?.model_id))
const selectedRuntime = computed(() => selectedInstance.value ? manager.runtimeForInstance(selectedInstance.value) : undefined)
const selectedTelemetry = computed<RuntimeTelemetry | undefined>(() => selectedInstance.value ? manager.telemetryForInstance(selectedInstance.value) : undefined)
const runtimeState = computed(() => selectedRuntime.value?.state || 'UNLOADED')
const isLoaded = computed(() => runtimeState.value === 'READY')
const instanceOptions = computed(() => manager.instances.value.map(instance => ({ label: instance.id, value: instance.id })))
const phaseLabel = computed(() => ({ cold: 'Cold start — autoload in progress', generating: 'Generating', completed: 'Completed', failed: 'Failed', '': '' }[phase.value]))

const panelItems = [
  { id: 'parameters' as const, label: 'Parameters' },
  { id: 'request' as const, label: 'Request' },
  { id: 'response' as const, label: 'Response' }
]

function runtimeVariant(state: string) {
  if (state === 'READY') return 'ready' as const
  if (state === 'FAILED') return 'failed' as const
  if (state === 'STARTING' || state === 'LOADING' || state === 'STOPPING') return 'pending' as const
  return 'neutral' as const
}

function selectInstance(value: unknown) {
  const id = String(value || '')
  if (!manager.instances.value.some(instance => instance.id === id)) return
  selectedInstanceID.value = id
  rawDirty.value = false
  syncRawRequest()
}

function stopValues(value = parameters.stop) {
  return value.split(/\r?\n|,/).map(item => item.trim()).filter(Boolean)
}

function parameterBody(messages: ThreadMessage[] = conversation.value) {
  const body: Record<string, unknown> = {
    model: selectedInstanceID.value,
    messages: [
      ...(parameters.systemPrompt.trim() ? [{ role: 'system', content: parameters.systemPrompt.trim() }] : []),
      ...messages.map(({ role, content }) => ({ role, content }))
    ],
    temperature: parameters.temperature,
    top_p: parameters.topP,
    max_tokens: parameters.maxTokens,
    top_k: parameters.topK,
    min_p: parameters.minP,
    repeat_penalty: parameters.repeatPenalty,
    stream: parameters.stream
  }
  const seed = Number(parameters.seed)
  if (parameters.seed.trim() !== '' && Number.isFinite(seed)) body.seed = seed
  const stop = stopValues()
  if (stop.length === 1) body.stop = stop[0]
  else if (stop.length > 1) body.stop = stop
  return body
}

function syncRawRequest() {
  if (rawDirty.value || !selectedInstanceID.value) return
  rawRequest.value = JSON.stringify(parameterBody(), null, 2)
}

function validMessages(value: unknown): ThreadMessage[] {
  if (!Array.isArray(value)) return []
  return value.flatMap((item: any) => {
    const role = String(item?.role || '') as Role
    const content = typeof item?.content === 'string' ? item.content : ''
    return ['system', 'user', 'assistant'].includes(role) && content ? [{ role, content }] : []
  })
}

function adoptBody(body: Record<string, any>) {
  const model = String(body.model || '').trim()
  if (model && manager.instances.value.some(item => item.id === model)) selectedInstanceID.value = model
  if (Number.isFinite(Number(body.temperature))) parameters.temperature = Number(body.temperature)
  if (Number.isFinite(Number(body.top_p))) parameters.topP = Number(body.top_p)
  if (Number.isFinite(Number(body.max_tokens))) parameters.maxTokens = Number(body.max_tokens)
  if (Number.isFinite(Number(body.top_k))) parameters.topK = Number(body.top_k)
  if (Number.isFinite(Number(body.min_p))) parameters.minP = Number(body.min_p)
  if (Number.isFinite(Number(body.repeat_penalty))) parameters.repeatPenalty = Number(body.repeat_penalty)
  parameters.seed = body.seed === undefined || body.seed === null ? '' : String(body.seed)
  parameters.stream = body.stream !== false
  parameters.stop = Array.isArray(body.stop) ? body.stop.join('\n') : typeof body.stop === 'string' ? body.stop : ''

  const messages = validMessages(body.messages)
  const system = messages.find(item => item.role === 'system')
  parameters.systemPrompt = system?.content || ''
  conversation.value = messages.filter(item => item.role !== 'system')
}

function requestBodyForSend() {
  let body: Record<string, any>
  if (rawDirty.value) {
    try {
      body = JSON.parse(rawRequest.value)
    } catch {
      throw new Error('Request JSON is not valid.')
    }
    if (!body || typeof body !== 'object' || Array.isArray(body)) throw new Error('Request JSON must be an object.')
  } else {
    body = parameterBody() as Record<string, any>
  }

  if (composer.value.trim()) {
    const messages = Array.isArray(body.messages) ? [...body.messages] : []
    messages.push({ role: 'user', content: composer.value.trim() })
    body.messages = messages
  }

  const target = String(body.model || '').trim()
  if (!target) body.model = selectedInstanceID.value
  else if (!manager.instances.value.some(item => item.id === target)) throw new Error(`Unknown Instance “${target}”.`)
  adoptBody(body)
  rawDirty.value = false
  rawRequest.value = JSON.stringify(body, null, 2)
  return body
}

const curlExample = computed(() => {
  const body = rawRequest.value || JSON.stringify(parameterBody(), null, 2)
  const escaped = body.replace(/'/g, `'"'"'`)
  return `curl ${manager.apiBase.value}/v1/chat/completions \\\n  -H 'Authorization: Bearer $LLAMA_API_KEY' \\\n  -H 'Content-Type: application/json' \\\n  -d '${escaped}'`
})

const sdkExample = computed(() => {
  const body = rawRequest.value || JSON.stringify(parameterBody(), null, 2)
  return `import json\nimport os\nfrom openai import OpenAI\n\nclient = OpenAI(\n    base_url="${manager.apiBase.value}/v1",\n    api_key=os.environ["LLAMA_API_KEY"],\n)\n\nbody = json.loads(${JSON.stringify(body)})\nresponse = client.chat.completions.create(**body)`
})

async function copyText(text: string, label: string) {
  notice.value = ''
  try {
    await navigator.clipboard.writeText(text)
    notice.value = `${label} copied.`
  } catch {
    error.value = `Unable to copy ${label.toLowerCase()}.`
  }
}

function clearConversation() {
  conversation.value = []
  composer.value = ''
  rawDirty.value = false
  diagnostics.value = null
  rawResponse.value = ''
  responseHeaders.value = []
  phase.value = ''
  error.value = ''
  notice.value = ''
  syncRawRequest()
}

function appendAssistant(content: string) {
  const index = conversation.value.length - 1
  const current = conversation.value[index]
  if (current?.role === 'assistant' && !current.stats) {
    current.content += content
    conversation.value = [...conversation.value]
  }
}

function consumeSSELine(line: string) {
  const trimmed = line.trim()
  if (!trimmed.startsWith('data:')) return
  const payload = trimmed.slice(5).trim()
  if (!payload || payload === '[DONE]') return
  try {
    const event = JSON.parse(payload)
    const choice = event?.choices?.[0]
    const content = choice?.delta?.content ?? choice?.message?.content ?? choice?.text
    if (typeof content === 'string') appendAssistant(content)
  } catch {
    // Raw response remains available for malformed/non-OpenAI-compatible frames.
  }
}

async function readStreamingResponse(response: Response) {
  if (!response.body) {
    rawResponse.value = await response.text()
    return
  }
  const reader = response.body.getReader()
  const decoder = new TextDecoder()
  let pending = ''
  while (true) {
    const { value, done } = await reader.read()
    if (done) break
    const chunk = decoder.decode(value, { stream: true })
    rawResponse.value += chunk
    pending += chunk
    const lines = pending.split(/\r?\n/)
    pending = lines.pop() || ''
    for (const line of lines) consumeSSELine(line)
  }
  pending += decoder.decode()
  if (pending) consumeSSELine(pending)
}

function responseErrorMessage(response: Response, body: string) {
  try {
    const parsed = JSON.parse(body)
    return parsed?.error?.message || parsed?.error || `Request failed with HTTP ${response.status}`
  } catch {
    return body.trim() || `Request failed with HTTP ${response.status}`
  }
}

async function loadDiagnostics(requestID: string) {
  let lastError: any
  for (let attempt = 0; attempt < 6; attempt++) {
    try {
      const result = await manager.request<PlaygroundDiagnostics>(`/api/v1/observability/playground/${encodeURIComponent(requestID)}`)
      diagnostics.value = result
      const last = [...conversation.value].reverse().find(item => item.role === 'assistant')
      if (last) {
        last.stats = {
          prompt: result.request.prompt_tokens || 0,
          completion: result.request.generated_tokens || 0,
          rate: result.request.tokens_per_second,
          ttft: result.request.ttft_ms
        }
        conversation.value = [...conversation.value]
      }
      return
    } catch (value) {
      lastError = value
      if (attempt < 5) await new Promise(resolve => setTimeout(resolve, 80))
    }
  }
  error.value ||= lastError?.data?.error || lastError?.message || 'Request completed, but diagnostics could not be loaded.'
}

async function send() {
  if (inFlight.value) return
  error.value = ''
  notice.value = ''
  if (!selectedInstance.value) {
    error.value = 'Select an Instance first.'
    return
  }
  const managementToken = readManagementToken()
  if (!managementToken) {
    error.value = 'Management session is unavailable. Sign in again.'
    return
  }

  let body: Record<string, any>
  try {
    body = requestBodyForSend()
  } catch (value: any) {
    error.value = value?.message || 'Unable to build request.'
    return
  }

  const target = manager.instances.value.find(item => item.id === String(body.model))
  if (!target) {
    error.value = 'The request model must be an existing Instance slug.'
    return
  }
  selectedInstanceID.value = target.id

  const sentMessages = validMessages(body.messages)
  conversation.value = sentMessages.filter(item => item.role !== 'system')
  conversation.value.push({ role: 'assistant', content: '' })
  composer.value = ''
  rawResponse.value = ''
  responseHeaders.value = []
  diagnostics.value = null
  activePanel.value = 'response'
  inFlight.value = true
  phase.value = manager.instanceState(target) === 'READY' ? 'generating' : 'cold'
  controller = new AbortController()
  let requestID = ''

  try {
    const response = await fetch(`${manager.apiBase.value}/api/v1/playground/chat/completions`, {
      method: 'POST',
      headers: {
        Authorization: `Bearer ${managementToken}`,
        'Content-Type': 'application/json'
      },
      body: JSON.stringify(body),
      signal: controller.signal
    })
    responseHeaders.value = Array.from(response.headers.entries())
    requestID = response.headers.get('X-LlamaCPP-Manager-Request-ID') || ''

    if (!response.ok) {
      const text = await response.text()
      rawResponse.value = text
      error.value = responseErrorMessage(response, text)
    } else if (body.stream !== false) {
      await readStreamingResponse(response)
    } else {
      const text = await response.text()
      rawResponse.value = text
      try {
        const parsed = JSON.parse(text)
        const content = parsed?.choices?.[0]?.message?.content ?? parsed?.choices?.[0]?.text
        if (typeof content === 'string') appendAssistant(content)
      } catch {
        // Keep the raw response visible even when it is not JSON.
      }
    }
    phase.value = response.ok ? 'completed' : 'failed'
  } catch (value: any) {
    if (value?.name === 'AbortError') {
      error.value = 'Request stopped.'
      phase.value = ''
    } else {
      error.value = value?.message || 'Inference request failed.'
      phase.value = 'failed'
    }
  } finally {
    inFlight.value = false
    controller = null
    if (requestID) await loadDiagnostics(requestID)
  }
}

function stop() {
  controller?.abort()
}

function onComposerKeydown(event: KeyboardEvent) {
  if (event.key !== 'Enter' || event.shiftKey) return
  event.preventDefault()
  void send()
}

function formatMS(value?: number) {
  if (!Number.isFinite(value)) return '—'
  const number = Number(value)
  return number < 1000 ? `${Math.round(number)} ms` : `${(number / 1000).toFixed(2)} s`
}

function formatRate(value?: number) {
  return Number.isFinite(value) ? `${Number(value).toFixed(2)} tok/s` : '—'
}

function formatBytes(value?: number) {
  if (!Number.isFinite(value)) return '—'
  return `${(Number(value) / 1024 ** 3).toFixed(2)} GiB`
}

const contextUsage = computed(() => {
  const used = diagnostics.value?.request.total_tokens
  const total = selectedModel.value?.context_length
  if (!Number.isFinite(used) || !Number.isFinite(total) || !total) return '—'
  return `${used} / ${total}`
})

const gpuAllocation = computed(() => {
  const telemetry = selectedTelemetry.value
  if (!telemetry) return '—'
  if (telemetry.gpus?.length) {
    return telemetry.gpus.map(gpu => `${gpu.device_id} ${formatBytes(gpu.vram_used_bytes)}`).join(' · ')
  }
  const devices = telemetry.gpu_devices || []
  return devices.length ? `${devices.join(', ')} · ${formatBytes(telemetry.vram_used_bytes)}` : '—'
})

const capturedHeaders = computed(() => responseHeaders.value)

watch(() => manager.instances.value, instances => {
  if (!instances.length) {
    selectedInstanceID.value = ''
    return
  }
  if (instances.some(item => item.id === selectedInstanceID.value)) return
  const query = Array.isArray(route.query.instance) ? route.query.instance[0] : route.query.instance
  selectedInstanceID.value = typeof query === 'string' && instances.some(item => item.id === query) ? query : (instances.find(item => item.enabled)?.id || instances[0]!.id)
}, { immediate: true, deep: true })

watch(runtimeState, state => {
  if (inFlight.value && phase.value === 'cold' && state === 'READY') phase.value = 'generating'
})

watch([selectedInstanceID, () => parameters.temperature, () => parameters.topP, () => parameters.maxTokens, () => parameters.seed, () => parameters.topK, () => parameters.minP, () => parameters.repeatPenalty, () => parameters.stop, () => parameters.stream, () => parameters.systemPrompt, conversation], syncRawRequest, { deep: true, immediate: true })

onBeforeUnmount(() => controller?.abort())
</script>

<template>
  <div class="space-y-4" data-testid="playground-page">
    <header class="border-b border-[var(--color-divider)] pb-4">
      <p class="text-[9.5px] font-extrabold tracking-[0.18em] text-[var(--neutral-700)]">PLAYGROUND</p>
      <div class="mt-1 flex flex-col gap-3 xl:flex-row xl:items-center xl:justify-between">
        <div class="flex flex-wrap items-center gap-2">
          <h1 class="min-w-0 break-all font-mono text-[16px] font-semibold text-[var(--neutral-900)] sm:text-[18px]">{{ selectedInstance?.id || 'Select an Instance' }}</h1>
          <StatusTag :variant="runtimeVariant(runtimeState)">{{ runtimeState }}</StatusTag>
          <StatusTag v-if="phaseLabel" :variant="phase === 'completed' ? 'ready' : phase === 'failed' ? 'failed' : 'pending'">{{ phaseLabel }}</StatusTag>
        </div>
        <div class="flex flex-wrap gap-1">
          <AppButton intent="ghost" size="sm" @click="copyText(curlExample, 'curl')">Copy as curl</AppButton>
          <AppButton intent="ghost" size="sm" @click="copyText(sdkExample, 'SDK example')">Copy SDK example</AppButton>
          <AppButton intent="ghost" size="sm" @click="clearConversation">Clear conversation</AppButton>
        </div>
      </div>
    </header>

    <Frame v-if="error" class="flex items-start gap-2 p-3" data-testid="playground-error">
      <StatusTag variant="failed">Request error</StatusTag>
      <p class="min-w-0 flex-1 text-xs leading-5 text-[var(--neutral-800)]">{{ error }}</p>
    </Frame>
    <p v-if="notice" class="border-y border-[var(--color-divider)] py-2 text-xs text-[var(--neutral-700)]">{{ notice }}</p>

    <div class="grid min-h-[calc(100vh-11rem)] gap-4 xl:grid-cols-[minmax(0,1fr)_340px]">
      <Frame class="flex min-h-[38rem] min-w-0 flex-col p-0" data-testid="playground-thread">
        <div class="sticky top-0 z-10 border-b border-[var(--color-divider)] bg-[var(--color-surface)] p-3 xl:hidden" data-testid="playground-mobile-controls">
          <div class="flex min-w-0 items-center gap-2">
            <USelect :model-value="selectedInstanceID" :items="instanceOptions" value-key="value" class="min-w-0 flex-1 font-mono" aria-label="Playground Instance" data-testid="playground-mobile-instance" @update:model-value="selectInstance" />
            <StatusTag :variant="runtimeVariant(runtimeState)">{{ runtimeState }}</StatusTag>
            <AppButton intent="secondary" size="sm" data-testid="playground-mobile-parameters-toggle" :aria-expanded="mobileParametersOpen" @click="mobileParametersOpen = !mobileParametersOpen">Parameters</AppButton>
          </div>
          <div v-if="mobileParametersOpen" class="mt-3 grid grid-cols-2 gap-3 border-t border-[var(--color-divider)] pt-3" data-testid="playground-mobile-quick-parameters">
            <UFormField label="temperature"><UInput v-model.number="parameters.temperature" data-testid="playground-mobile-temperature" type="number" step="0.05" class="font-mono tabular-nums" /></UFormField>
            <UFormField label="top_p"><UInput v-model.number="parameters.topP" data-testid="playground-mobile-top-p" type="number" step="0.05" class="font-mono tabular-nums" /></UFormField>
            <UFormField label="max_tokens"><UInput v-model.number="parameters.maxTokens" data-testid="playground-mobile-max-tokens" type="number" class="font-mono tabular-nums" /></UFormField>
            <div class="flex items-end pb-2"><UCheckbox v-model="parameters.stream" label="stream" data-testid="playground-mobile-stream" /></div>
            <p class="col-span-2 text-xs leading-5 text-[var(--neutral-700)]">Quick controls stay beside the composer on mobile. Advanced parameters and raw Request/Response inspection remain below the thread.</p>
          </div>
        </div>

        <div class="min-h-0 flex-1 space-y-5 overflow-auto p-5">
          <div v-if="parameters.systemPrompt.trim()" class="max-w-3xl font-mono text-[12.5px] text-[var(--neutral-700)]">
            <p class="mb-1 text-[9.5px] font-extrabold tracking-[0.18em]">system</p>
            <p class="whitespace-pre-wrap">{{ parameters.systemPrompt }}</p>
          </div>

          <div
            v-for="(message, index) in conversation"
            :key="`${index}-${message.role}`"
            class="flex"
            :class="message.role === 'user' ? 'justify-end' : 'justify-start'"
          >
            <article
              class="max-w-[82%] px-4 py-3"
              :class="message.role === 'user'
                ? 'bg-[var(--neutral-200)]'
                : 'border-l-2 border-[var(--accent-300)] bg-transparent'"
            >
              <p class="mb-1 text-[9.5px] font-extrabold tracking-[0.18em] text-[var(--neutral-700)]">{{ message.role }}</p>
              <p class="whitespace-pre-wrap text-sm leading-6">{{ message.content || (message.role === 'assistant' && inFlight ? '…' : '') }}</p>
              <p v-if="message.role === 'assistant' && message.stats" class="mt-2 font-mono text-[11px] tabular-nums text-[var(--neutral-700)]">
                {{ message.stats.prompt }} prompt · {{ message.stats.completion }} completion · {{ formatRate(message.stats.rate) }} · ttft {{ formatMS(message.stats.ttft) }}
              </p>
            </article>
          </div>

          <div v-if="!parameters.systemPrompt.trim() && !conversation.length" class="grid min-h-56 place-items-center text-center">
            <div>
              <p class="text-sm font-semibold">Exercise an Instance through the real gateway.</p>
              <p class="mt-1 max-w-lg text-xs leading-5 text-[var(--neutral-700)]">Requests here use the signed-in management session to re-enter the public OpenAI-compatible gateway, preserving the same lifecycle and observability path as external clients.</p>
            </div>
          </div>
        </div>

        <div class="border-t border-[var(--color-divider)] p-4">
          <p v-if="selectedInstance && !isLoaded" class="mb-2 text-xs text-[var(--neutral-700)]">This Instance is not loaded — sending will trigger autoload through the gateway.</p>
          <div class="flex items-end gap-2">
            <textarea
              v-model="composer"
              class="min-h-24 flex-1 resize-y border border-[var(--color-divider)] bg-transparent px-3 py-2 font-mono text-[13px] outline-none focus:border-[var(--color-accent)]"
              placeholder="Message"
              aria-label="Playground message"
              @keydown="onComposerKeydown"
            />
            <AppButton v-if="!inFlight" intent="primary" :disabled="!selectedInstance" @click="send">Send</AppButton>
            <AppButton v-else intent="secondary" @click="stop">Stop</AppButton>
          </div>
        </div>
      </Frame>

      <aside class="min-w-0 space-y-4" data-testid="playground-rail">
        <Frame class="p-0">
          <div class="hidden border-b border-[var(--color-divider)] p-4 xl:block">
            <p class="text-[9.5px] font-extrabold tracking-[0.18em] text-[var(--neutral-700)]">Instance — the OpenAI model value</p>
            <div class="mt-3 max-h-56 space-y-1 overflow-auto">
              <button
                v-for="instance in manager.instances.value"
                :key="instance.id"
                type="button"
                class="block w-full border px-3 py-2 text-left"
                :class="selectedInstanceID === instance.id
                  ? 'border-[var(--color-accent)] bg-[var(--color-accent)] text-[var(--color-on-accent)]'
                  : 'border-[var(--color-divider)] bg-transparent'"
                @click="selectInstance(instance.id)"
              >
                <span class="block font-mono text-[12px] font-semibold">{{ instance.id }}</span>
                <span class="mt-0.5 block text-[10px] opacity-75">{{ manager.instanceState(instance) }} · {{ manager.models.value.find(model => model.id === instance.model_id)?.name || instance.model_id }}</span>
              </button>
            </div>
          </div>

          <div class="grid grid-cols-3 border-b border-[var(--color-divider)]">
            <button
              v-for="item in panelItems"
              :key="item.id"
              type="button"
              class="border-r border-[var(--color-divider)] px-2 py-2 text-[11px] font-semibold last:border-r-0"
              :class="activePanel === item.id ? 'bg-[var(--accent-100)] text-[var(--accent-800)]' : 'text-[var(--neutral-700)]'"
              @click="activePanel = item.id"
            >
              {{ item.label }}
            </button>
          </div>

          <div v-if="activePanel === 'parameters'" class="space-y-4 p-4" data-testid="playground-parameters">
            <div class="grid grid-cols-2 gap-3">
              <label class="text-[10px] text-[var(--neutral-700)]">temperature<UInput v-model.number="parameters.temperature" type="number" step="0.05" class="mt-1 font-mono tabular-nums" /></label>
              <label class="text-[10px] text-[var(--neutral-700)]">top_p<UInput v-model.number="parameters.topP" type="number" step="0.05" class="mt-1 font-mono tabular-nums" /></label>
              <label class="text-[10px] text-[var(--neutral-700)]">max_tokens<UInput v-model.number="parameters.maxTokens" type="number" class="mt-1 font-mono tabular-nums" /></label>
              <label class="text-[10px] text-[var(--neutral-700)]">seed<UInput v-model="parameters.seed" type="number" class="mt-1 font-mono tabular-nums" /></label>
              <label class="text-[10px] text-[var(--neutral-700)]">top_k<UInput v-model.number="parameters.topK" type="number" class="mt-1 font-mono tabular-nums" /></label>
              <label class="text-[10px] text-[var(--neutral-700)]">min_p<UInput v-model.number="parameters.minP" type="number" step="0.01" class="mt-1 font-mono tabular-nums" /></label>
              <label class="text-[10px] text-[var(--neutral-700)]">repeat_penalty<UInput v-model.number="parameters.repeatPenalty" type="number" step="0.05" class="mt-1 font-mono tabular-nums" /></label>
              <label class="text-[10px] text-[var(--neutral-700)]">stop<UInput v-model="parameters.stop" class="mt-1 font-mono" placeholder="one, two" /></label>
            </div>
            <UCheckbox v-model="parameters.stream" label="stream" />
            <label class="block text-[10px] text-[var(--neutral-700)]">system prompt
              <textarea v-model="parameters.systemPrompt" class="mt-1 min-h-28 w-full resize-y border border-[var(--color-divider)] bg-transparent p-2 font-mono text-[12px] outline-none focus:border-[var(--color-accent)]" />
            </label>
          </div>

          <div v-else-if="activePanel === 'request'" class="space-y-4 p-4" data-testid="playground-request">
            <label class="block text-[9.5px] font-extrabold tracking-[0.18em] text-[var(--neutral-700)]">RAW JSON</label>
            <textarea
              v-model="rawRequest"
              class="min-h-80 w-full resize-y border border-[var(--color-divider)] bg-transparent p-3 font-mono text-[11px] leading-5 outline-none focus:border-[var(--color-accent)]"
              aria-label="Raw request JSON"
              @input="rawDirty = true"
            />
            <div>
              <p class="mb-2 text-[9.5px] font-extrabold tracking-[0.18em] text-[var(--neutral-700)]">CURL</p>
              <pre class="overflow-auto bg-[var(--neutral-200)] p-3 font-mono text-[10.5px] leading-5 whitespace-pre-wrap">{{ curlExample }}</pre>
            </div>
          </div>

          <div v-else class="space-y-4 p-4" data-testid="playground-response">
            <template v-if="rawResponse || capturedHeaders.length">
              <div>
                <p class="mb-2 text-[9.5px] font-extrabold tracking-[0.18em] text-[var(--neutral-700)]">RESPONSE HEADERS</p>
                <pre class="max-h-44 overflow-auto bg-[var(--neutral-200)] p-3 font-mono text-[10.5px] leading-5 whitespace-pre-wrap">{{ capturedHeaders.map(([key, value]) => `${key}: ${value}`).join('\n') }}</pre>
              </div>
              <div>
                <p class="mb-2 text-[9.5px] font-extrabold tracking-[0.18em] text-[var(--neutral-700)]">RAW {{ parameters.stream ? 'SSE STREAM' : 'RESPONSE' }}</p>
                <pre class="max-h-72 overflow-auto bg-[var(--neutral-200)] p-3 font-mono text-[10.5px] leading-5 whitespace-pre-wrap">{{ rawResponse }}</pre>
              </div>
            </template>
            <p v-else class="py-8 text-center text-xs text-[var(--neutral-700)]">Send a request to capture the raw response.</p>
          </div>
        </Frame>

        <Frame class="p-4" data-testid="playground-diagnostics">
          <p class="text-[9.5px] font-extrabold tracking-[0.18em] text-[var(--neutral-700)]">REQUEST DIAGNOSTICS</p>
          <dl v-if="diagnostics" class="mt-3 divide-y divide-[var(--color-divider)] text-[11px]">
            <div class="grid grid-cols-[110px_1fr] gap-2 py-2"><dt class="text-[var(--neutral-700)]">Instance</dt><dd class="font-mono tabular-nums">{{ diagnostics.request.instance_id || '—' }}</dd></div>
            <div class="grid grid-cols-[110px_1fr] gap-2 py-2"><dt class="text-[var(--neutral-700)]">Instance state</dt><dd class="font-mono tabular-nums">{{ diagnostics.state_trace?.join(' → ') || '—' }}</dd></div>
            <div class="grid grid-cols-[110px_1fr] gap-2 py-2"><dt class="text-[var(--neutral-700)]">Cold start</dt><dd>{{ diagnostics.request.autoloaded ? 'yes — autoload' : 'no' }}</dd></div>
            <div class="grid grid-cols-[110px_1fr] gap-2 py-2"><dt class="text-[var(--neutral-700)]">Startup time</dt><dd class="font-mono tabular-nums">{{ formatMS(diagnostics.request.load_duration_ms) }}</dd></div>
            <div class="grid grid-cols-[110px_1fr] gap-2 py-2"><dt class="text-[var(--neutral-700)]">TTFT</dt><dd class="font-mono tabular-nums">{{ formatMS(diagnostics.request.ttft_ms) }}</dd></div>
            <div class="grid grid-cols-[110px_1fr] gap-2 py-2"><dt class="text-[var(--neutral-700)]">Generation time</dt><dd class="font-mono tabular-nums">{{ formatMS(Math.max(0, diagnostics.request.duration_ms - (diagnostics.request.ttft_ms || 0))) }}</dd></div>
            <div class="grid grid-cols-[110px_1fr] gap-2 py-2"><dt class="text-[var(--neutral-700)]">Prompt tokens</dt><dd class="font-mono tabular-nums">{{ diagnostics.request.prompt_tokens }}</dd></div>
            <div class="grid grid-cols-[110px_1fr] gap-2 py-2"><dt class="text-[var(--neutral-700)]">Generated tokens</dt><dd class="font-mono tabular-nums">{{ diagnostics.request.generated_tokens }}</dd></div>
            <div class="grid grid-cols-[110px_1fr] gap-2 py-2"><dt class="text-[var(--neutral-700)]">Tokens / second</dt><dd class="font-mono tabular-nums">{{ formatRate(diagnostics.request.tokens_per_second) }}</dd></div>
            <div class="grid grid-cols-[110px_1fr] gap-2 py-2"><dt class="text-[var(--neutral-700)]">Context usage</dt><dd class="font-mono tabular-nums">{{ contextUsage }}</dd></div>
            <div class="grid grid-cols-[110px_1fr] gap-2 py-2"><dt class="text-[var(--neutral-700)]">GPU allocation</dt><dd class="font-mono tabular-nums">{{ gpuAllocation }}</dd></div>
            <div class="grid grid-cols-[110px_1fr] gap-2 py-2"><dt class="text-[var(--neutral-700)]">Evictions triggered</dt><dd class="font-mono tabular-nums">{{ diagnostics.evictions_triggered?.join(', ') || 'none' }}</dd></div>
          </dl>
          <p v-else class="mt-3 text-xs leading-5 text-[var(--neutral-700)]">Send a request to record lifecycle and inference diagnostics for this Instance.</p>
        </Frame>
      </aside>
    </div>

    <p class="border-t border-[var(--color-divider)] pt-3 text-xs leading-5 text-[var(--neutral-700)]">Playground requests use the signed-in management session through an internal `/api/v1` bridge that re-enters the public inference gateway, so instance resolution, autoload, eviction and logging behave exactly as they do for external clients. These figures are live diagnostics, not a benchmark.</p>
  </div>
</template>