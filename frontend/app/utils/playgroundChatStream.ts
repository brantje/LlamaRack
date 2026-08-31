export const PLAYGROUND_GENERATING_PLACEHOLDER = 'Generating…'
export const PLAYGROUND_REASONING_ONLY_FALLBACK = 'The model returned reasoning but no visible text.'
export const PLAYGROUND_EMPTY_CONTENT_FALLBACK = 'The model returned no visible text.'
export const PLAYGROUND_TRUNCATION_WARNING = 'Response truncated — the model stopped at max_tokens.'

export type ChatDelta = {
  text?: string
  reasoning?: string
  finishReason?: string
}

function asRecord(value: unknown): Record<string, unknown> | null {
  return value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : null
}

function stringField(value: unknown): string | undefined {
  return typeof value === 'string' && value ? value : undefined
}

function contentText(value: unknown): string | undefined {
  const direct = stringField(value)
  if (direct) return direct
  if (!Array.isArray(value)) return undefined
  let combined = ''
  for (const item of value) {
    const record = asRecord(item)
    const piece = stringField(record?.text)
    if (piece) combined += piece
  }
  return combined || undefined
}

export function extractChatDelta(choice: unknown): ChatDelta {
  const item = asRecord(choice)
  if (!item) return {}
  const delta = asRecord(item.delta)
  const message = asRecord(item.message)
  const source = delta || message || item
  const text = contentText(source.content) || contentText(message?.content) || stringField(item.text)
  const reasoning = stringField(source.reasoning_content)
    || stringField(source.reasoning)
    || stringField(source.thinking)
    || stringField(message?.reasoning_content)
    || stringField(message?.reasoning)
    || stringField(message?.thinking)
  const finishReason = stringField(item.finish_reason) || stringField(delta?.finish_reason) || stringField(message?.finish_reason)
  const result: ChatDelta = {}
  if (text) result.text = text
  if (reasoning) result.reasoning = reasoning
  if (finishReason) result.finishReason = finishReason
  return result
}

export function parseSSEDataLine(line: string): ChatDelta | null {
  const trimmed = line.trim()
  if (!trimmed.startsWith('data:')) return null
  const payload = trimmed.slice(5).trim()
  if (!payload || payload === '[DONE]') return null
  try {
    return extractChatDelta(JSON.parse(payload)?.choices?.[0])
  } catch {
    return null
  }
}

export function playgroundEmptyContentFallback(hasReasoning: boolean) {
  return hasReasoning ? PLAYGROUND_REASONING_ONLY_FALLBACK : PLAYGROUND_EMPTY_CONTENT_FALLBACK
}

export function isLengthFinishReason(value?: string) {
  return value === 'length'
}
