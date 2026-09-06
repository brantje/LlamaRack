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
import { newPlaygroundSessionID } from '~/utils/playgroundSession'

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
const reuseSession = ref(true)
const chatSessionID = ref(newPlaygroundSessionID())
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
const hasComposerPayload = computed(() => Boolean(composer.value.trim()) || attachments.value.length > 0)
const canSend = computed(() => Boolean(selectedInstance.value) && (hasComposerPayload.value || rawDirty.value))
const submitDisabled = computed(() => !inFlight.value && !canSend.value)
const chatStatus = computed<ChatStatus>(() => {
  if (inFlight.value) {
    const last = conversation.value.at(-1)
    if (last?.role === 'assistant' && last.parts.some(part => (part.type === 'text' || part.type === 'reasoning') && part.text)) return 'streaming'
    return 'submitted'
  }
  return 'ready'
})
const promptSubmitLabel = computed(() => (
  chatStatus.value === 'submitted' || chatStatus.value === 'streaming' ? 'Stop generating' : 'Send prompt'
))
const chatIndicatorLabel = computed(() => {
  if (!inFlight.value) return ''
  if (phase.value === 'cold') return 'Cold start — autoload in progress'
  return PLAYGROUND_GENERATING_PLACEHOLDER
})
const showGenerationStatus = computed(() => {
  if (!inFlight.value) return false
  const last = conversation.value.at(-1)
  if (last?.role === 'assistant' && (assistantHasText(last.parts) || assistantHasReasoning(last.parts))) return false
  return Boolean(chatIndicatorLabel.value)
})
const chatMessages = computed(() => conversation.value.map(message => ({
  id: message.id,
  role: message.role,
  parts: message.parts.length
    ? message.parts.map(part => ({ ...part }))
    : [{ type: 'text' as const, text: '', state: inFlight.value && message.role === 'assistant' ? 'streaming' as const : undefined }]
})))

const panelItems = [
  { label: 'Parameters', value: 'parameters', slot: 'parameters' },
  { label: 'Request', value: 'request', slot: 'request' },
  { label: 'Response', value: 'response', slot: 'response' },
  { label: 'Session', value: 'session', slot: 'session' },
]

const chatPromptUi = {
  root: 'relative flex w-full flex-col items-stretch gap-2 rounded-none bg-[var(--color-surface)] px-2.5 py-2 ring ring-[var(--color-divider)] has-[textarea:focus-visible]:ring-[var(--color-divider)] has-[textarea:focus-visible]:outline-none'
}
const chatMessagesUi = {
  root: 'min-h-0 w-full flex-1 overflow-y-auto px-2.5 [&>article]:last-of-type:min-h-0'
}
const userMessageUi = {
  content: 'rounded-none bg-[var(--color-accent)] text-[var(--color-on-accent)] ring ring-[var(--color-accent)]'
}

function resetChatSession() {
  chatSessionID.value = reuseSession.value ? newPlaygroundSessionID() : ''
}

watch(reuseSession, () => {
  resetChatSession()
})

function runtimeVariant(state: string) {
  if (state === 'READY') return 'ready' as const
  if (state === 'FAILED') return 'failed' as const
  if (state === 'STARTING' || state === 'LOADING' || state === 'DRAINING' || state === 'STOPPING') return 'pending' as const
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

function sessionRequestHeaders() {
  if (!reuseSession.value || !chatSessionID.value) return {}
  return { 'X-LiteLLM-Session-ID': chatSessionID.value }
}

const curlExample = computed(() => {
  const body = rawRequest.value || JSON.stringify(parameterBody(), null, 2)
  const escaped = body.replace(/'/g, `'"'"'`)
  const sessionHeader = reuseSession.value && chatSessionID.value
    ? ` \\\n  -H 'X-LiteLLM-Session-ID: ${chatSessionID.value}'`
    : ''
  return `curl ${manager.apiBase.value}/v1/chat/completions \\\n  -H 'Authorization: Bearer $LLAMA_API_KEY' \\\n  -H 'Content-Type: application/json'${sessionHeader} \\\n  -d '${escaped}'`
})

const sdkExample = computed(() => {
  const body = rawRequest.value || JSON.stringify(parameterBody(), null, 2)
  const extraHeaders = reuseSession.value && chatSessionID.value
    ? `,\n    extra_headers={"X-LiteLLM-Session-ID": ${JSON.stringify(chatSessionID.value)}}`
    : ''
  return `import json\nimport os\nfrom openai import OpenAI\n\nclient = OpenAI(\n    base_url="${manager.apiBase.value}/v1",\n    api_key=os.environ["LLAMA_API_KEY"],\n)\n\nbody = json.loads(${JSON.stringify(body)})\nresponse = client.chat.completions.create(**body${extraHeaders})`
})

async function copyText(text: string, label: string) {
  notice.value = ''
  try {
    await navigator.clipboard.writeText(text)
    notice.value = `${label} copied.`
  } catch {
    notice.value = `Unable to copy ${label.toLowerCase()}.`
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
  resetChatSession()
  syncRawRequest()
}

async function requestClearConversation() {
  const confirmed = await confirmation.value?.request({
    title: 'Clear chat',
    description: 'This removes all messages, diagnostics, and the captured raw request/response from this Playground session. This cannot be undone.',
    confirmLabel: 'Clear chat',
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

async function send(options: { allowEmpty?: boolean } = {}) {
  if (inFlight.value) return
  if (!options.allowEmpty && !hasComposerPayload.value && !rawDirty.value) return
  inFlight.value = true
  error.value = ''
  notice.value = ''
  if (!selectedInstance.value) {
    error.value = 'Select an Instance first.'
    inFlight.value = false
    return
  }
  const managementToken = readManagementToken()
  if (!managementToken) {
    error.value = 'Management session is unavailable. Sign in again.'
    inFlight.value = false
    return
  }

  let body: Record<string, any>
  try {
    body = await requestBodyForSendAsync()
  } catch (value: any) {
    error.value = value?.message || 'Unable to build request.'
    inFlight.value = false
    return
  }

  const target = manager.instances.value.find(item => item.id === String(body.model))
  if (!target) {
    error.value = 'The request model must be an existing Instance slug.'
    inFlight.value = false
    return
  }
  selectedInstanceID.value = target.id

  conversation.value.push(toThreadMessage('assistant', [{ type: 'text', text: '', state: 'streaming' }], `assistant-${conversation.value.length}`))
  composer.value = ''
  clearAttachments()
  rawResponse.value = ''
  responseHeaders.value = []
  diagnostics.value = null
  phase.value = manager.instanceState(target) === 'READY' ? 'generating' : 'cold'
  controller = new AbortController()
  let requestID = ''

  try {
    const response = await fetch(`${manager.apiBase.value}/api/v1/playground/chat/completions`, {
      method: 'POST',
      headers: {
        Authorization: `Bearer ${managementToken}`,
        'Content-Type': 'application/json',
        ...sessionRequestHeaders()
      },
      body: JSON.stringify(body),
      signal: controller.signal
    })
    responseHeaders.value = Array.from(response.headers.entries())
    requestID = response.headers.get('X-LlamaRack-Request-ID') || ''

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
  if (chatStatus.value === 'submitted' || chatStatus.value === 'streaming') {
    stop()
    return
  }
  event.preventDefault()
  void send()
}

function isLastAssistant(id: string) {
  const last = conversation.value.at(-1)
  return last?.role === 'assistant' && last.id === id
}

async function copyAssistantMessage(id: string) {
  const message = conversation.value.find(item => item.id === id)
  if (!message) return
  const text = message.parts
    .filter((part): part is Extract<ChatPart, { type: 'text' }> => part.type === 'text')
    .map(part => part.text)
    .join('')
    .trim()
  if (!text) return
  await copyText(text, 'Message')
}

async function regenerate() {
  if (inFlight.value) return
  const last = conversation.value.at(-1)
  if (last?.role === 'assistant') conversation.value = conversation.value.slice(0, -1)
  await send({ allowEmpty: true })
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

const capturedHeaders = computed(() => responseHeaders.value.filter(([key]) => key.toLowerCase() !== 'x-llamarack-upstream-port'))

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
  <div
    class="flex h-[calc(100dvh-8.5rem)] min-h-0 flex-col gap-3 overflow-hidden lg:h-[calc(100dvh-5rem)]"
    data-testid="playground-page"
  >
    <header class="shrink-0 border-b border-[var(--color-divider)] pb-3">
      <div class="flex flex-col gap-2 xl:flex-row xl:items-center xl:justify-between">
        <UPageHeader title="PLAYGROUND" headline="OpenAI-compatible gateway" />
        <div class="flex flex-wrap gap-1">
          <AppButton intent="ghost" size="sm" @click="copyText(curlExample, 'curl')">Copy as curl</AppButton>
          <AppButton intent="ghost" size="sm" @click="copyText(sdkExample, 'SDK example')">Copy SDK example</AppButton>
          <AppButton intent="ghost" size="sm" data-testid="playground-clear-conversation" @click="requestClearConversation">Clear chat</AppButton>
        </div>
      </div>
    </header>

    <Frame v-if="error" class="w-full shrink-0 p-3" data-testid="playground-error">
      <div class="flex items-start gap-2">
        <StatusTag variant="failed">Request error</StatusTag>
        <p class="min-w-0 flex-1 whitespace-pre-wrap break-words text-xs leading-5 text-[var(--neutral-800)]">{{ error }}</p>
      </div>
    </Frame>
    <p v-if="notice" class="shrink-0 border-y border-[var(--color-divider)] py-2 text-xs text-[var(--neutral-700)]" data-testid="playground-notice">{{ notice }}</p>

    <div class="grid min-h-0 flex-1 gap-4 overflow-y-auto xl:grid-cols-[minmax(0,1fr)_24rem] xl:items-stretch xl:overflow-hidden">
      <Frame
        class="flex min-h-[calc(100dvh-15rem)] min-w-0 flex-col overflow-hidden p-0 xl:h-full xl:min-h-0"
        data-testid="playground-thread"
      >
        <div class="shrink-0 border-b border-[var(--color-divider)] bg-[var(--color-surface)] p-3" data-testid="playground-thread-chrome">
          <div class="flex min-w-0 items-center gap-2">
            <USelect
              :model-value="selectedInstanceID"
              :items="instanceOptions"
              value-key="value"
              class="min-w-0 flex-1 font-mono xl:hidden"
              aria-label="Playground Instance"
              data-testid="playground-mobile-instance"
              @update:model-value="selectInstance"
            />
            <StatusTag :variant="runtimeVariant(runtimeState)">{{ runtimeState === 'READY' ? 'Instance READY' : runtimeState }}</StatusTag>
            <span
              data-testid="playground-model-name"
              class="min-w-0 truncate font-mono text-[length:var(--font-size-h5)] font-semibold"
            >{{ selectedModel?.name || selectedInstance?.model_id || 'Select an Instance' }}</span>
            <StatusTag v-if="phase === 'failed'" variant="failed">{{ phaseLabel }}</StatusTag>
            <StatusTag v-else-if="phase === 'completed'" variant="ready">{{ phaseLabel }}</StatusTag>
          </div>
        </div>

        <div class="flex min-h-0 flex-1 flex-col overflow-hidden">
          <div
            v-if="parameters.systemPrompt.trim()"
            class="shrink-0 border-b border-[var(--color-divider)] px-5 py-3"
          >
            <p class="mb-1 font-mono text-[length:var(--font-size-kicker)] font-extrabold tracking-[0.18em] text-[var(--neutral-700)]">system</p>
            <p class="whitespace-pre-wrap font-mono text-[length:var(--font-size-h6)] text-[var(--neutral-700)]">{{ parameters.systemPrompt }}</p>
          </div>

          <UEmpty
            v-if="!conversation.length"
            variant="naked"
            class="flex min-h-0 flex-1 items-center justify-center"
            title="Type a prompt to start."
            description="The composer stays at the bottom of this thread. Attach an image or type a message, then send."
            data-testid="playground-empty-state"
          />

          <UChatMessages
            v-else
            :messages="chatMessages"
            :status="chatStatus"
            compact
            should-auto-scroll
            :auto-scroll="{ 'aria-label': 'Scroll to latest messages' }"
            :assistant="{ side: 'left', variant: 'naked' }"
            :user="{ side: 'right', variant: 'solid', ui: userMessageUi }"
            :ui="chatMessagesUi"
            class="min-h-0 flex-1 overflow-y-auto"
            data-testid="playground-chat-messages"
          >
            <template #indicator>
              <div v-if="showGenerationStatus" data-testid="playground-chat-indicator">
                <StatusTag variant="pending" data-testid="playground-phase-label">{{ chatIndicatorLabel }}</StatusTag>
              </div>
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
                v-show="part.text"
                class="whitespace-pre-wrap text-sm leading-6"
                :data-testid="message.role === 'assistant' ? 'playground-assistant-text' : 'playground-user-text'"
              >
                {{ part.text }}
              </p>
              <p
                v-if="emptyContentFallback(message)"
                class="whitespace-pre-wrap text-sm leading-6 text-[var(--neutral-800)]"
                data-testid="playground-empty-content"
              >
                {{ emptyContentFallback(message) }}
              </p>
              <div
                v-if="messageTruncated(message.id)"
                class="mt-2 flex items-start gap-2"
              >
                <StatusTag variant="pending">Truncated</StatusTag>
                <p
                  class="min-w-0 text-xs leading-5 text-[var(--neutral-800)]"
                  data-testid="playground-truncation-warning"
                >
                  {{ PLAYGROUND_TRUNCATION_WARNING }}
                </p>
              </div>
              <p
                v-if="message.role === 'assistant' && messageStats(message.id)"
                class="mt-2 font-mono text-[length:var(--font-size-table-header)] tabular-nums text-[var(--neutral-700)]"
                data-testid="playground-token-stats"
              >
                {{ messageStats(message.id)?.prompt }} prompt ·
                {{ messageStats(message.id)?.completion }} completion (incl. reasoning) ·
                {{ formatRate(messageStats(message.id)?.rate) }} ·
                ttft {{ formatMS(messageStats(message.id)?.ttft) }}
              </p>
              <div v-if="message.role === 'assistant' && !inFlight" class="mt-2 flex flex-wrap gap-1">
                <AppButton intent="ghost" size="xs" data-testid="playground-copy-message" @click="copyAssistantMessage(message.id)">Copy</AppButton>
                <AppButton
                  v-if="isLastAssistant(message.id)"
                  intent="ghost"
                  size="xs"
                  data-testid="playground-regenerate"
                  @click="regenerate"
                >Regenerate</AppButton>
              </div>
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
            :ui="chatPromptUi"
            @submit="() => send()"
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
                <div class="flex items-center gap-1">
                  <AppButton
                    intent="ghost"
                    size="sm"
                    icon="i-lucide-plus"
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
                  :disabled="submitDisabled"
                  :aria-label="promptSubmitLabel"
                  submitted-icon="i-lucide-square"
                  streaming-icon="i-lucide-square"
                  square
                  type="button"
                  data-testid="playground-prompt-submit"
                  @click="onPromptAction"
                  @stop="stop"
                />
              </div>
            </template>
          </UChatPrompt>
        </div>
      </Frame>

      <aside class="flex min-h-0 min-w-0 flex-col gap-4 xl:overflow-y-auto" data-testid="playground-rail">
        <Frame class="p-0">
          <div class="hidden border-b border-[var(--color-divider)] p-4 xl:block" data-testid="playground-instance-list">
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
          <UTabs
            v-model="activePanel"
            :items="panelItems"
            :unmount-on-hide="false"
            variant="link"
            class="w-full gap-0"
            :ui="{ list: 'border-b border-[var(--color-divider)] px-2', trigger: 'grow' }"
            aria-label="Playground inspector"
          >
            <template #parameters>
              <div class="space-y-4 p-4" data-testid="playground-parameters">
                <div class="grid grid-cols-2 gap-3">
                  <UFormField label="temperature" description="Sampling randomness. 0 is near-deterministic.">
                    <UInput v-model.number="parameters.temperature" type="number" step="0.05" class="font-mono tabular-nums" />
                  </UFormField>
                  <UFormField label="top_p" description="Nucleus sampling cutoff.">
                    <UInput v-model.number="parameters.topP" type="number" step="0.05" class="font-mono tabular-nums" />
                  </UFormField>
                  <UFormField label="max_tokens" description="Maximum generated tokens, including reasoning.">
                    <UInput v-model.number="parameters.maxTokens" type="number" class="font-mono tabular-nums" />
                  </UFormField>
                  <UFormField label="seed" description="Optional integer for reproducible sampling.">
                    <UInput v-model="parameters.seed" type="number" class="font-mono tabular-nums" />
                  </UFormField>
                  <UFormField label="top_k">
                    <UInput v-model.number="parameters.topK" type="number" class="font-mono tabular-nums" />
                  </UFormField>
                  <UFormField label="min_p">
                    <UInput v-model.number="parameters.minP" type="number" step="0.01" class="font-mono tabular-nums" />
                  </UFormField>
                  <UFormField label="repeat_penalty">
                    <UInput v-model.number="parameters.repeatPenalty" type="number" step="0.05" class="font-mono tabular-nums" />
                  </UFormField>
                  <UFormField label="stop" description="Stop sequences as a comma list, or one per line.">
                    <UInput v-model="parameters.stop" class="font-mono" placeholder="token, or one per line" />
                  </UFormField>
                </div>
                <UCheckbox v-model="parameters.stream" label="stream" />
               
                <UFormField label="system prompt" description="Sent as a system message before the thread.">
                  <UTextarea v-model="parameters.systemPrompt" :rows="6" autoresize class="w-full font-mono text-[length:var(--font-size-h6)]" />
                </UFormField>
              </div>
            </template>
            <template #request>
              <div class="space-y-4 p-4" data-testid="playground-request">
                <UFormField label="RAW JSON">
                  <UTextarea
                    v-model="rawRequest"
                    :rows="12"
                    autoresize
                    class="w-full font-mono text-[length:var(--font-size-table-header)] leading-5"
                    aria-label="Raw request JSON"
                    @update:model-value="rawDirty = true"
                  />
                </UFormField>
                <div>
                  <p class="mb-2 text-[length:var(--font-size-kicker)] font-extrabold tracking-[0.18em] text-[var(--neutral-700)]">CURL</p>
                  <pre class="max-h-56 overflow-auto bg-[var(--neutral-200)] p-3 font-mono text-[length:var(--font-size-table-header)] leading-5 whitespace-pre-wrap">{{ curlExample }}</pre>
                </div>
              </div>
            </template>
            <template #response>
              <div class="space-y-4 p-4" data-testid="playground-response">
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
            </template>
            <template #session>
              <div class="space-y-4 p-4" data-testid="playground-session">
                <UFormField
                  label="Reuse session"
                  description="Follow-up sends share one session ID while this page stays open. Turn off for a new session on every request."
                >
                  <USwitch v-model="reuseSession" data-testid="playground-reuse-session" aria-label="Reuse session" />
                </UFormField>
                <p
                  v-if="reuseSession && chatSessionID"
                  data-testid="playground-session-id"
                  class="min-w-0 break-all font-mono text-[length:var(--font-size-table-header)]"
                >{{ chatSessionID }}</p>
              </div>
            </template>
          </UTabs>
        </Frame>

        <Frame class="p-4" data-testid="playground-diagnostics">
          <p class="text-[length:var(--font-size-kicker)] font-extrabold tracking-[0.18em] text-[var(--neutral-700)]">REQUEST DIAGNOSTICS</p>
          <dl v-if="diagnostics" class="mt-3 divide-y divide-[var(--color-divider)] text-[length:var(--font-size-table-header)]">
            <div class="grid grid-cols-[110px_1fr] gap-2 py-2"><dt class="text-[var(--neutral-700)]">Session ID</dt><dd data-testid="playground-diagnostics-session" class="min-w-0 break-all font-mono text-[length:var(--font-size-table-header)]">{{ chatSessionID || '—' }}</dd></div>
            <div class="grid grid-cols-[110px_1fr] gap-2 py-2"><dt class="text-[var(--neutral-700)]">Instance</dt><dd class="font-mono tabular-nums">{{ diagnostics.request.instance_id || '—' }}</dd></div>
            <div class="grid grid-cols-[110px_1fr] gap-2 py-2"><dt class="text-[var(--neutral-700)]">Instance state</dt><dd class="font-mono tabular-nums">{{ diagnostics.state_trace?.join(' → ') || '—' }}</dd></div>
            <div class="grid grid-cols-[110px_1fr] gap-2 py-2"><dt class="text-[var(--neutral-700)]">Cold start</dt><dd>{{ diagnostics.request.autoloaded ? 'yes — autoload' : 'no' }}</dd></div>
            <div class="grid grid-cols-[110px_1fr] gap-2 py-2"><dt class="text-[var(--neutral-700)]">Startup time</dt><dd class="font-mono tabular-nums">{{ formatMS(diagnostics.request.load_duration_ms) }}</dd></div>
            <div class="grid grid-cols-[110px_1fr] gap-2 py-2"><dt class="text-[var(--neutral-700)]">TTFT</dt><dd class="font-mono tabular-nums">{{ formatMS(diagnostics.request.ttft_ms) }}</dd></div>
            <div class="grid grid-cols-[110px_1fr] gap-2 py-2"><dt class="text-[var(--neutral-700)]">Generation time</dt><dd class="font-mono tabular-nums">{{ formatMS(Math.max(0, diagnostics.request.duration_ms - (diagnostics.request.ttft_ms || 0))) }}</dd></div>
            <div class="grid grid-cols-[110px_1fr] gap-2 py-2"><dt class="text-[var(--neutral-700)]">Prompt tokens</dt><dd class="font-mono tabular-nums">{{ diagnostics.request.prompt_tokens }}</dd></div>
            <div class="grid grid-cols-[110px_1fr] gap-2 py-2"><dt class="text-[var(--neutral-700)]">Generated tokens (incl. reasoning)</dt><dd class="font-mono tabular-nums">{{ diagnostics.request.generated_tokens }}</dd></div>
            <div class="grid grid-cols-[110px_1fr] gap-2 py-2"><dt class="text-[var(--neutral-700)]">Tokens / second</dt><dd class="font-mono tabular-nums">{{ formatRate(diagnostics.request.tokens_per_second) }}</dd></div>
            <div class="grid grid-cols-[110px_1fr] gap-2 py-2"><dt class="text-[var(--neutral-700)]">Context usage</dt><dd class="font-mono tabular-nums">{{ contextUsage }}</dd></div>
            <div class="grid grid-cols-[110px_1fr] gap-2 py-2"><dt class="text-[var(--neutral-700)]">GPU allocation</dt><dd class="font-mono tabular-nums">{{ gpuAllocation }}</dd></div>
            <div class="grid grid-cols-[110px_1fr] gap-2 py-2"><dt class="text-[var(--neutral-700)]">Evictions triggered</dt><dd class="font-mono tabular-nums">{{ diagnostics.evictions_triggered?.join(', ') || 'none' }}</dd></div>
          </dl>
          <p v-else class="mt-3 text-xs leading-5 text-[var(--neutral-700)]">Send a request to record lifecycle and inference diagnostics for this Instance.</p>
        </Frame>
      </aside>
    </div>

    <UCollapsible class="shrink-0" data-testid="playground-about">
      <AppButton intent="ghost" size="xs">About Playground requests</AppButton>
      <template #content>
        <p class="border-t border-[var(--color-divider)] pt-3 text-xs leading-5 text-[var(--neutral-700)]">Playground requests use the signed-in management session through an internal `/api/v1` bridge that re-enters the public inference gateway, so instance resolution, autoload, eviction and logging behave exactly as they do for external clients. These figures are live diagnostics, not a benchmark.</p>
      </template>
    </UCollapsible>
    <AppConfirmationModal ref="confirmation" />
  </div>
</template>

<style scoped>
/* Nuxt UI ChatPrompt still merges a decorative blur utility. Naming that utility in
   class would fail the design-rule scanner, so force an opaque composer surface here. */
:deep([data-testid='playground-composer'] form) {
  backdrop-filter: none;
}

:deep([data-testid='playground-composer'] form:has(textarea:focus-visible)) {
  outline: none;
  --tw-ring-color: var(--color-accent);
}
</style>