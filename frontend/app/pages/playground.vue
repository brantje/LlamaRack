<script setup lang="ts">
import type { Instance, Model, RuntimeTelemetry } from '~/composables/useManager'
import { readManagementToken } from '~/composables/useManagerApi'
import {
  PLAYGROUND_ATTACHMENT_ACCEPT,
  PLAYGROUND_MAX_ATTACHMENT_BYTES,
  PLAYGROUND_MAX_ATTACHMENTS,
  buildApiMessageContent,
  isPlaygroundImageType,
  parseApiMessageContent,
  readFileAsDataUrl,
  threadPartsToApiContent
} from '~/utils/playgroundMessageContent'
import {
  PLAYGROUND_GENERATING_PLACEHOLDER,
  PLAYGROUND_TRUNCATION_WARNING,
  type ChatDelta,
  extractChatDelta,
  isLengthFinishReason,
  parseSSEDataLine,
  playgroundEmptyContentFallback
} from '~/utils/playgroundChatStream'
import { isPartStreaming } from '@nuxt/ui/utils/ai'

type Role = 'system' | 'user' | 'assistant'
type ChatStatus = 'ready' | 'submitted' | 'streaming' | 'error'
type MessageStats = { prompt: number; completion: number; rate?: number; ttft?: number }
type FileChatPart = { type: 'file', url: string, mediaType: string, filename: string }
type ChatPart = { type: 'text' | 'reasoning', text: string, state?: 'streaming' } | FileChatPart
type ThreadMessage = { id: string, role: Role, parts: ChatPart[], stats?: MessageStats, finishReason?: string }
type PendingAttachment = {
  id: string
  file: File
  previewUrl: string
  mediaType: string
  filename: string
}
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
const attachments = ref<PendingAttachment[]>([])
const fileInputRef = ref<HTMLInputElement | null>(null)
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
const confirmation = ref<{ request: (options: {
  title: string
  description: string
  confirmLabel?: string
  cancelLabel?: string
  confirmIntent?: 'primary' | 'secondary' | 'ghost'
  confirmTone?: 'default' | 'destructive'
}) => Promise<boolean> } | null>(null)
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
const phaseLabel = computed(() => ({ cold: 'Cold start — autoload in progress', generating: 'Generating', completed: 'Completed', failed: 'Last request failed', '': '' }[phase.value]))
const chatIndicatorLabel = computed(() => phase.value === 'cold' ? 'Cold start — autoload in progress' : PLAYGROUND_GENERATING_PLACEHOLDER)
const chatStatus = computed<ChatStatus>(() => {
  if (inFlight.value) {
    const last = conversation.value.at(-1)
    if (last?.role === 'assistant' && last.parts.some(part => (part.type === 'text' || part.type === 'reasoning') && part.text)) return 'streaming'
    return 'submitted'
  }
  return 'ready'
})
const chatMessages = computed(() => conversation.value.map(message => ({
  id: message.id,
  role: message.role,
  parts: message.parts.length
    ? message.parts.map(part => ({ ...part }))
    : [{ type: 'text' as const, text: '', state: inFlight.value && message.role === 'assistant' ? 'streaming' as const : undefined }]
})))

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

function messageText(message: Pick<ThreadMessage, 'parts'>) {
  return message.parts
    .filter((part): part is Extract<ChatPart, { type: 'text' | 'reasoning' }> => part.type === 'text' || part.type === 'reasoning')
    .map(part => part.text)
    .join('')
}

let messageCounter = 0

function nextMessageId(prefix: string) {
  messageCounter += 1
  return `${prefix}-${messageCounter}`
}

function toThreadMessage(role: Role, parts: ChatPart[], id = nextMessageId(role)): ThreadMessage {
  return { id, role, parts }
}

function parseBodyMessages(value: unknown): Array<{ role: Role, parts: ChatPart[] }> {
  if (!Array.isArray(value)) return []
  const messages: Array<{ role: Role, parts: ChatPart[] }> = []
  for (const item of value) {
    const role = String(item?.role || '') as Role
    if (!['system', 'user', 'assistant'].includes(role)) continue
    if (role === 'system') {
      const content = typeof item.content === 'string' ? item.content.trim() : ''
      if (content) messages.push({ role, parts: [{ type: 'text', text: content }] })
      continue
    }
    const parts = parseApiMessageContent(item.content) as ChatPart[]
    if (parts.length) messages.push({ role, parts })
  }
  return messages
}

function parameterBody(messages: ThreadMessage[] = conversation.value) {
  const body: Record<string, unknown> = {
    model: selectedInstanceID.value,
    messages: [
      ...(parameters.systemPrompt.trim() ? [{ role: 'system', content: parameters.systemPrompt.trim() }] : []),
      ...messages.map(message => ({ role: message.role, content: threadPartsToApiContent(message.parts) }))
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

  const messages = parseBodyMessages(body.messages)
  const system = messages.find(item => item.role === 'system')
  parameters.systemPrompt = system ? messageText({ parts: system.parts }) : ''
  conversation.value = messages
    .filter(item => item.role !== 'system')
    .map((item, index) => toThreadMessage(item.role, item.parts, `${item.role}-${index}`))
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

  return body
}

async function requestBodyForSendAsync() {
  const body = requestBodyForSend()
  let encodedAttachments: Array<{ dataUrl: string, mediaType: string }>
  try {
    encodedAttachments = await Promise.all(
      attachments.value.map(async attachment => ({
        dataUrl: await readFileAsDataUrl(attachment.file),
        mediaType: attachment.mediaType
      }))
    )
  } catch {
    throw new Error('Unable to read one or more attachments.')
  }

  if (composer.value.trim() || encodedAttachments.length) {
    const messages = Array.isArray(body.messages) ? [...body.messages] : []
    messages.push({
      role: 'user',
      content: buildApiMessageContent(composer.value, encodedAttachments)
    })
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

function revokeAttachmentPreview(attachment: PendingAttachment) {
  if (!attachment.previewUrl) return
  URL.revokeObjectURL(attachment.previewUrl)
}

function clearAttachments() {
  for (const attachment of attachments.value) revokeAttachmentPreview(attachment)
  attachments.value = []
  if (fileInputRef.value) fileInputRef.value.value = ''
}

function removeAttachment(id: string) {
  const index = attachments.value.findIndex(attachment => attachment.id === id)
  if (index < 0) return
  const [removed] = attachments.value.splice(index, 1)
  if (removed) revokeAttachmentPreview(removed)
}

async function onAttachmentInput(event: Event) {
  error.value = ''
  const input = event.target as HTMLInputElement
  const files = Array.from(input.files ?? [])
  input.value = ''
  for (const file of files) {
    if (!isPlaygroundImageType(file.type)) {
      error.value = 'Only image files can be attached in Playground.'
      continue
    }
    if (file.size > PLAYGROUND_MAX_ATTACHMENT_BYTES) {
      error.value = `“${file.name}” exceeds the 8 MiB attachment limit.`
      continue
    }
    if (attachments.value.length >= PLAYGROUND_MAX_ATTACHMENTS) {
      error.value = `Playground supports up to ${PLAYGROUND_MAX_ATTACHMENTS} images per message.`
      break
    }
    attachments.value.push({
      id: nextMessageId('attachment'),
      file,
      previewUrl: safePreviewUrl(file),
      mediaType: file.type,
      filename: file.name
    })
  }
}

function safePreviewUrl(file: File) {
  try {
    return URL.createObjectURL(file)
  } catch {
    return ''
  }
}

function clearConversation() {
  conversation.value = []
  composer.value = ''
  clearAttachments()
  rawDirty.value = false
  diagnostics.value = null
  rawResponse.value = ''
  responseHeaders.value = []
  phase.value = ''
  error.value = ''
  notice.value = ''
  syncRawRequest()
}

async function requestClearConversation() {
  const confirmed = await confirmation.value?.request({
    title: 'Clear conversation',
    description: 'This removes all messages, diagnostics, and the captured raw request/response from this Playground session. This cannot be undone.',
    confirmLabel: 'Clear conversation',
    confirmTone: 'destructive'
  })
  if (!confirmed) return
  stop()
  clearConversation()
}

function replaceAssistantMessage(index: number, message: ThreadMessage) {
  conversation.value = [...conversation.value.slice(0, index), message, ...conversation.value.slice(index + 1)]
}

function appendAssistantPart(type: 'text' | 'reasoning', content: string) {
  const index = conversation.value.length - 1
  const current = conversation.value[index]
  if (current?.role !== 'assistant' || current.stats) return
  const parts = current.parts.map(part => ({ ...part }))
  const streamingMatch = [...parts].reverse().find(part => (part.type === 'text' || part.type === 'reasoning') && part.type === type && part.state === 'streaming')
  const emptyMatch = parts.find(part => (part.type === 'text' || part.type === 'reasoning') && part.type === type && !part.text)
  const target = streamingMatch || emptyMatch
  if (target && (target.type === 'text' || target.type === 'reasoning')) {
    target.text += content
    target.state = 'streaming'
  } else {
    parts.push({ type, text: content, state: 'streaming' })
  }
  replaceAssistantMessage(index, { ...current, parts })
}

function setAssistantFinishReason(reason: string) {
  const index = conversation.value.length - 1
  const current = conversation.value[index]
  if (current?.role !== 'assistant' || current.stats) return
  replaceAssistantMessage(index, { ...current, finishReason: reason })
}

function finalizeStreamingParts() {
  const index = conversation.value.length - 1
  const current = conversation.value[index]
  if (current?.role !== 'assistant') return
  const parts = current.parts
    .map(part => {
      if ((part.type === 'text' || part.type === 'reasoning') && part.state === 'streaming') {
        const next = { ...part }
        delete next.state
        return next
      }
      return { ...part }
    })
    .filter(part => part.type === 'file' || Boolean(part.text))
  if (!parts.length && phase.value !== 'completed') {
    conversation.value = conversation.value.slice(0, index)
    return
  }
  replaceAssistantMessage(index, { ...current, parts })
}

function applyChatDelta(delta: ChatDelta) {
  if (delta.reasoning) appendAssistantPart('reasoning', delta.reasoning)
  if (delta.text) appendAssistantPart('text', delta.text)
  if (delta.finishReason) setAssistantFinishReason(delta.finishReason)
}

function consumeChoicePayload(choice: unknown) {
  applyChatDelta(extractChatDelta(choice))
}

function consumeSSELine(line: string) {
  const delta = parseSSEDataLine(line)
  if (!delta) return
  applyChatDelta(delta)
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
    await nextTick()
  }
  pending += decoder.decode()
  if (pending) consumeSSELine(pending)
  await nextTick()
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
    body = await requestBodyForSendAsync()
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

  conversation.value.push(toThreadMessage('assistant', [{ type: 'text', text: '', state: 'streaming' }], nextMessageId('assistant')))
  composer.value = ''
  clearAttachments()
  rawResponse.value = ''
  responseHeaders.value = []
  diagnostics.value = null
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
      activePanel.value = 'response'
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
        consumeChoicePayload(parsed?.choices?.[0])
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
    finalizeStreamingParts()
    inFlight.value = false
    controller = null
    if (requestID) await loadDiagnostics(requestID)
  }
}

function stop() {
  controller?.abort()
}

function onPromptAction(event: MouseEvent) {
  if (chatStatus.value === 'submitted' || chatStatus.value === 'streaming') return
  event.preventDefault()
  void send()
}

function messageStats(id: string) {
  return conversation.value.find(item => item.id === id)?.stats
}

function messageReasoningParts(parts: ChatPart[]) {
  return parts.filter(part => part.type === 'reasoning')
}

function messageTextParts(parts: ChatPart[]) {
  return parts.filter(part => part.type === 'text')
}

function assistantHasText(parts: ChatPart[]) {
  return parts.some(part => part.type === 'text' && part.text)
}

function assistantHasReasoning(parts: ChatPart[]) {
  return parts.some(part => part.type === 'reasoning' && part.text)
}

function generatingPlaceholder(message: { role: string, parts: ChatPart[] }) {
  if (message.role !== 'assistant' || !inFlight.value || assistantHasText(message.parts) || assistantHasReasoning(message.parts)) return ''
  return PLAYGROUND_GENERATING_PLACEHOLDER
}

function emptyContentFallback(message: { role: string, parts: ChatPart[] }) {
  if (message.role !== 'assistant' || inFlight.value || assistantHasText(message.parts) || phase.value !== 'completed') return ''
  return playgroundEmptyContentFallback(assistantHasReasoning(message.parts))
}

function messageTruncated(id: string) {
  return isLengthFinishReason(conversation.value.find(item => item.id === id)?.finishReason)
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

onBeforeUnmount(() => {
  controller?.abort()
  clearAttachments()
})
</script>

<template>
  <div class="flex min-h-[calc(100dvh-12rem)] flex-col gap-4" data-testid="playground-page">
    <header class="shrink-0 border-b border-[var(--color-divider)] pb-4">
      <div class="mt-1 flex flex-col gap-3 xl:flex-row xl:items-center xl:justify-between">
        <UPageHeader title="PLAYGROUND" headline="OpenAI-compatible gateway" description="Exercise an Instance through the real gateway." />

      
        <div class="flex flex-wrap gap-1">
          <AppButton intent="ghost" size="sm" @click="copyText(curlExample, 'curl')">Copy as curl</AppButton>
          <AppButton intent="ghost" size="sm" @click="copyText(sdkExample, 'SDK example')">Copy SDK example</AppButton>
          <AppButton intent="ghost" size="sm" data-testid="playground-clear-conversation" @click="requestClearConversation">Clear conversation</AppButton>
        </div>
      </div>
    </header>

    <Frame v-if="error" class="flex items-start gap-2 p-3" data-testid="playground-error">
      <StatusTag variant="failed">Request error</StatusTag>
      <p class="min-w-0 flex-1 text-xs leading-5 text-[var(--neutral-800)]">{{ error }}</p>
    </Frame>
    <p v-if="notice" class="border-y border-[var(--color-divider)] py-2 text-xs text-[var(--neutral-700)]">{{ notice }}</p>

    <div class="grid min-h-0 flex-1 gap-4 xl:grid-cols-[minmax(0,1fr)_340px] xl:items-stretch">
      <Frame class="flex h-full min-h-0 min-w-0 flex-col p-0" data-testid="playground-thread">
        <div class="sticky top-0 z-10 shrink-0 border-b border-[var(--color-divider)] bg-[var(--color-surface)] p-3 xl:hidden" data-testid="playground-mobile-controls">
          <div class="flex min-w-0 items-center gap-2">
            <USelect :model-value="selectedInstanceID" :items="instanceOptions" value-key="value" class="min-w-0 flex-1 font-mono" aria-label="Playground Instance" data-testid="playground-mobile-instance" @update:model-value="selectInstance" />
            <StatusTag :variant="runtimeVariant(runtimeState)">{{ runtimeState }}</StatusTag>
            <StatusTag v-if="inFlight && phaseLabel" variant="pending" data-testid="playground-mobile-phase">{{ phaseLabel }}</StatusTag>
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

        <div class="flex min-h-0 flex-1 flex-col overflow-hidden">
          <div
            v-if="parameters.systemPrompt.trim()"
            class="shrink-0 border-b border-[var(--color-divider)] px-5 py-4"
          >
            <p class="mb-1 font-mono text-[length:var(--font-size-kicker)] font-extrabold tracking-[0.18em] text-[var(--neutral-700)]">system</p>
            <p class="whitespace-pre-wrap font-mono text-[length:var(--font-size-h6)] text-[var(--neutral-700)]">{{ parameters.systemPrompt }}</p>
          </div>

          <UEmpty
            v-if="!conversation.length"
            variant="naked"
            class="flex min-h-0 flex-1 items-center justify-center"
            title="Exercise an Instance through the real gateway."
            description="Requests here use the signed-in management session to re-enter the public OpenAI-compatible gateway, preserving the same lifecycle and observability path as external clients."
          />

          <UChatMessages
            v-else
            :messages="chatMessages"
            :status="chatStatus"
            should-auto-scroll
            :assistant="{ side: 'left', variant: 'naked' }"
            :user="{ side: 'right', variant: 'solid', ui: { content: 'bg-[var(--color-accent)] text-[var(--color-on-accent)]' } }"
            class="min-h-0 flex-1"
            data-testid="playground-chat-messages"
          >
            <template #indicator>
              <p class="text-sm text-[var(--neutral-800)]" data-testid="playground-chat-indicator">{{ chatIndicatorLabel }}</p>
            </template>
            <template #files="{ parts }">
              <img
                v-for="(part, index) in parts"
                :key="`${part.url}-${index}`"
                :src="part.url"
                :alt="part.filename || 'attachment'"
                class="max-h-48 max-w-full border border-[var(--color-divider)] object-contain"
              >
            </template>
            <template #content="{ message }">
              <UChatReasoning
                v-for="(part, index) in messageReasoningParts(message.parts)"
                :key="`${message.id}-reasoning-${index}`"
                :text="part.text"
                :streaming="isPartStreaming(part)"
                data-testid="playground-reasoning"
              />
              <p
                v-for="(part, index) in messageTextParts(message.parts)"
                :key="`${message.id}-text-${index}`"
                v-show="part.text || generatingPlaceholder(message)"
                class="whitespace-pre-wrap text-sm leading-6"
                :data-testid="message.role === 'assistant' ? 'playground-assistant-text' : 'playground-user-text'"
              >
                {{ part.text || generatingPlaceholder(message) }}
              </p>
              <p
                v-if="emptyContentFallback(message)"
                class="whitespace-pre-wrap text-sm leading-6 text-[var(--neutral-800)]"
                data-testid="playground-empty-content"
              >
                {{ emptyContentFallback(message) }}
              </p>
              <p
                v-if="messageTruncated(message.id)"
                class="mt-2 text-xs leading-5 text-[var(--neutral-800)]"
                data-testid="playground-truncation-warning"
              >
                {{ PLAYGROUND_TRUNCATION_WARNING }}
              </p>
              <p
                v-if="message.role === 'assistant' && messageStats(message.id)"
                class="mt-2 font-mono text-[length:var(--font-size-table-header)] tabular-nums text-[var(--neutral-700)]"
              >
                {{ messageStats(message.id)?.prompt }} prompt ·
                {{ messageStats(message.id)?.completion }} completion ·
                {{ formatRate(messageStats(message.id)?.rate) }} ·
                ttft {{ formatMS(messageStats(message.id)?.ttft) }}
              </p>
            </template>
          </UChatMessages>
        </div>

        <div class="mt-auto shrink-0 border-t border-[var(--color-divider)] p-4" data-testid="playground-composer">
          <p v-if="selectedInstance && !isLoaded" class="mb-2 text-xs text-[var(--neutral-700)]">This Instance is not loaded — sending will trigger autoload through the gateway.</p>
          <UChatPrompt
            v-model="composer"
            aria-label="Playground message"
            :disabled="!selectedInstance"
            class="w-full"
            @submit="send"
          >
            <template v-if="attachments.length" #header>
              <div class="flex flex-wrap gap-2">
                <UButton
                  v-for="attachment in attachments"
                  :key="attachment.id"
                  :label="attachment.filename"
                  :avatar="attachment.previewUrl ? { src: attachment.previewUrl } : undefined"
                  :icon="attachment.previewUrl ? undefined : 'i-lucide-image'"
                  color="neutral"
                  variant="soft"
                  size="xs"
                  trailing-icon="i-lucide-x"
                  @click="removeAttachment(attachment.id)"
                />
              </div>
            </template>
            <template #footer>
              <div class="flex w-full items-center justify-between gap-2">
                <div>
                  <UButton
                    icon="i-lucide-plus"
                    color="neutral"
                    variant="ghost"
                    size="sm"
                    aria-label="Attach images"
                    data-testid="playground-attach-files"
                    :disabled="!selectedInstance"
                    @click="fileInputRef?.click()"
                  />
                  <input
                    ref="fileInputRef"
                    type="file"
                    :accept="PLAYGROUND_ATTACHMENT_ACCEPT"
                    multiple
                    class="hidden"
                    data-testid="playground-file-input"
                    @change="onAttachmentInput"
                  >
                </div>
                <UChatPromptSubmit
                  :status="chatStatus"
                  :disabled="!selectedInstance"
                  data-testid="playground-prompt-submit"
                  @click="onPromptAction"
                  @stop="stop"
                />
              </div>
            </template>
          </UChatPrompt>
        </div>
      </Frame>

      <aside class="min-w-0 space-y-4" data-testid="playground-rail">
        <Frame class="p-0">
          <div class="hidden border-b border-[var(--color-divider)] p-4 xl:block">
            <p class="text-[length:var(--font-size-kicker)] font-extrabold tracking-[0.18em] text-[var(--neutral-700)]">Instance — the OpenAI model value</p>
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
                <span class="block font-mono text-[length:var(--font-size-h6)] font-semibold">{{ instance.id }}</span>
                <span class="mt-0.5 block text-[length:var(--font-size-kicker)] opacity-75">{{ manager.instanceState(instance) }} · {{ manager.models.value.find(model => model.id === instance.model_id)?.name || instance.model_id }}</span>
              </button>
            </div>
          </div>

          <div class="grid grid-cols-3 border-b border-[var(--color-divider)]" role="tablist" aria-label="Playground inspector">
            <button
              v-for="item in panelItems"
              :key="item.id"
              type="button"
              role="tab"
              :aria-selected="activePanel === item.id"
              class="border-r border-[var(--color-divider)] px-2 py-2 text-[length:var(--font-size-table-header)] font-semibold last:border-r-0"
              :class="activePanel === item.id ? 'bg-[var(--accent-100)] text-[var(--accent-800)]' : 'text-[var(--neutral-700)]'"
              @click="activePanel = item.id"
            >
              {{ item.label }}
            </button>
          </div>

          <div v-if="activePanel === 'parameters'" class="space-y-4 p-4" data-testid="playground-parameters">
            <div class="grid grid-cols-2 gap-3">
              <UFormField label="temperature"><UInput v-model.number="parameters.temperature" type="number" step="0.05" class="font-mono tabular-nums" /></UFormField>
              <UFormField label="top_p"><UInput v-model.number="parameters.topP" type="number" step="0.05" class="font-mono tabular-nums" /></UFormField>
              <UFormField label="max_tokens"><UInput v-model.number="parameters.maxTokens" type="number" class="font-mono tabular-nums" /></UFormField>
              <UFormField label="seed"><UInput v-model="parameters.seed" type="number" class="font-mono tabular-nums" /></UFormField>
              <UFormField label="top_k"><UInput v-model.number="parameters.topK" type="number" class="font-mono tabular-nums" /></UFormField>
              <UFormField label="min_p"><UInput v-model.number="parameters.minP" type="number" step="0.01" class="font-mono tabular-nums" /></UFormField>
              <UFormField label="repeat_penalty"><UInput v-model.number="parameters.repeatPenalty" type="number" step="0.05" class="font-mono tabular-nums" /></UFormField>
              <UFormField label="stop"><UInput v-model="parameters.stop" class="font-mono" placeholder="one, two" /></UFormField>
            </div>
            <UCheckbox v-model="parameters.stream" label="stream" />
            <UFormField label="system prompt">
              <UTextarea v-model="parameters.systemPrompt" :rows="6" autoresize class="w-full font-mono text-[length:var(--font-size-h6)]" />
            </UFormField>
          </div>

          <div v-else-if="activePanel === 'request'" class="space-y-4 p-4" data-testid="playground-request">
            <label class="block text-[length:var(--font-size-kicker)] font-extrabold tracking-[0.18em] text-[var(--neutral-700)]">RAW JSON</label>
            <textarea
              v-model="rawRequest"
              class="min-h-80 w-full resize-y border border-[var(--color-divider)] bg-transparent p-3 font-mono text-[length:var(--font-size-table-header)] leading-5 outline-none focus:border-[var(--color-accent)]"
              aria-label="Raw request JSON"
              @input="rawDirty = true"
            />
            <div>
              <p class="mb-2 text-[length:var(--font-size-kicker)] font-extrabold tracking-[0.18em] text-[var(--neutral-700)]">CURL</p>
              <pre class="overflow-auto bg-[var(--neutral-200)] p-3 font-mono text-[length:var(--font-size-table-header)] leading-5 whitespace-pre-wrap">{{ curlExample }}</pre>
            </div>
          </div>

          <div v-else class="space-y-4 p-4" data-testid="playground-response">
            <template v-if="rawResponse || capturedHeaders.length">
              <div>
                <p class="mb-2 text-[length:var(--font-size-kicker)] font-extrabold tracking-[0.18em] text-[var(--neutral-700)]">RESPONSE HEADERS</p>
                <pre class="max-h-44 overflow-auto bg-[var(--neutral-200)] p-3 font-mono text-[length:var(--font-size-table-header)] leading-5 whitespace-pre-wrap">{{ capturedHeaders.map(([key, value]) => `${key}: ${value}`).join('\n') }}</pre>
              </div>
              <div>
                <p class="mb-2 text-[length:var(--font-size-kicker)] font-extrabold tracking-[0.18em] text-[var(--neutral-700)]">RAW {{ parameters.stream ? 'SSE STREAM' : 'RESPONSE' }}</p>
                <pre class="max-h-72 overflow-auto bg-[var(--neutral-200)] p-3 font-mono text-[length:var(--font-size-table-header)] leading-5 whitespace-pre-wrap">{{ rawResponse }}</pre>
              </div>
            </template>
            <p v-else class="py-8 text-center text-xs text-[var(--neutral-700)]">Send a request to capture the raw response.</p>
          </div>
        </Frame>

        <Frame class="p-4" data-testid="playground-diagnostics">
          <p class="text-[length:var(--font-size-kicker)] font-extrabold tracking-[0.18em] text-[var(--neutral-700)]">REQUEST DIAGNOSTICS</p>
          <dl v-if="diagnostics" class="mt-3 divide-y divide-[var(--color-divider)] text-[length:var(--font-size-table-header)]">
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

    <p class="shrink-0 border-t border-[var(--color-divider)] pt-3 text-xs leading-5 text-[var(--neutral-700)]">Playground requests use the signed-in management session through an internal `/api/v1` bridge that re-enters the public inference gateway, so instance resolution, autoload, eviction and logging behave exactly as they do for external clients. These figures are live diagnostics, not a benchmark.</p>
    <AppConfirmationModal ref="confirmation" />
  </div>
</template>