export type InferenceTurnStats = {
  prompt_n?: number
  prompt_ms?: number
  prompt_per_second?: number
  prompt_per_token_ms?: number
  predicted_n?: number
  predicted_ms?: number
  predicted_per_second?: number
  predicted_per_token_ms?: number
  cache_n?: number
  draft_n?: number
  draft_n_accepted?: number
  finish_reason?: string
  tool_call_count?: number
}

export type PlaygroundDiagnosticRequest = {
  duration_ms?: number
  ttft_ms?: number
  prompt_tokens?: number
  generated_tokens?: number
  total_tokens?: number
  tokens_per_second?: number
  prompt_tokens_per_second?: number
  generation_tokens_per_second?: number
  queue_duration_ms?: number
}

export type PlaygroundDiagnosticsLike = {
  request: PlaygroundDiagnosticRequest
  inference_stats?: InferenceTurnStats
}

export type PlaygroundTurnStats = {
  promptTokens?: number
  generatedTokens?: number
  totalTokens?: number
  promptRate?: number
  generationRate?: number
  queueMs?: number
  wallMs?: number
  ttftMs?: number
  promptMs?: number
  promptPerTokenMs?: number
  generationMs?: number
  generationPerTokenMs?: number
  cacheTokens?: number
  cachePercent?: number
  draftProposed?: number
  draftAccepted?: number
  draftAcceptancePercent?: number
  contextUsed?: number
  contextMax?: number
  contextPercent?: number
  finishReason?: string
  toolCallCount?: number
  wallEstimated?: boolean
  ttftEstimated?: boolean
}

function finite(value: unknown): number | undefined {
  return typeof value === 'number' && Number.isFinite(value) ? value : undefined
}

function nonNegative(value: unknown): number | undefined {
  const number = finite(value)
  return number !== undefined && number >= 0 ? number : undefined
}

function positive(value: unknown): number | undefined {
  const number = finite(value)
  return number !== undefined && number > 0 ? number : undefined
}

function trimmed(value: unknown): string | undefined {
  return typeof value === 'string' && value.trim() ? value.trim() : undefined
}

function percent(numerator: number | undefined, denominator: number | undefined): number | undefined {
  if (numerator === undefined || denominator === undefined || denominator <= 0) return undefined
  return (numerator / denominator) * 100
}

export function mergePlaygroundTurnStats(...sources: Array<PlaygroundTurnStats | undefined>): PlaygroundTurnStats {
  const merged: PlaygroundTurnStats = {}
  for (const source of sources) {
    if (!source) continue
    for (const [key, value] of Object.entries(source) as Array<[keyof PlaygroundTurnStats, PlaygroundTurnStats[keyof PlaygroundTurnStats]]>) {
      if (value !== undefined) (merged as Record<string, unknown>)[key] = value
    }
  }
  return merged
}

export function playgroundTurnStatsFromDiagnostics(
  diagnostics: PlaygroundDiagnosticsLike,
  contextMax?: number,
  fallbackFinishReason?: string
): PlaygroundTurnStats {
  const request = diagnostics.request || {}
  const inference = diagnostics.inference_stats || {}
  const promptTokens = nonNegative(inference.prompt_n) ?? positive(request.prompt_tokens)
  const generatedTokens = nonNegative(inference.predicted_n) ?? positive(request.generated_tokens)
  const totalTokens = positive(request.total_tokens)
  const cacheTokens = nonNegative(inference.cache_n)
  const promptMs = nonNegative(inference.prompt_ms)
  const generationMs = nonNegative(inference.predicted_ms)
  const promptRate = nonNegative(inference.prompt_per_second) ?? positive(request.prompt_tokens_per_second)
  const generationRate = nonNegative(inference.predicted_per_second)
    ?? positive(request.generation_tokens_per_second)
    ?? positive(request.tokens_per_second)
  const promptPerTokenMs = nonNegative(inference.prompt_per_token_ms)
    ?? (promptMs !== undefined && promptTokens !== undefined && promptTokens > 0 ? promptMs / promptTokens : undefined)
  const generationPerTokenMs = nonNegative(inference.predicted_per_token_ms)
    ?? (generationMs !== undefined && generatedTokens !== undefined && generatedTokens > 0 ? generationMs / generatedTokens : undefined)
  const draftProposed = nonNegative(inference.draft_n)
  const draftAccepted = nonNegative(inference.draft_n_accepted)
  const authoritativeContext = promptTokens !== undefined && cacheTokens !== undefined && generatedTokens !== undefined
    ? promptTokens + cacheTokens + generatedTokens
    : undefined
  const contextUsed = authoritativeContext ?? totalTokens
  const normalizedContextMax = positive(contextMax)
  const promptCacheTotal = promptTokens !== undefined && cacheTokens !== undefined ? promptTokens + cacheTokens : undefined

  return {
    promptTokens,
    generatedTokens,
    totalTokens,
    promptRate,
    generationRate,
    queueMs: nonNegative(request.queue_duration_ms),
    wallMs: nonNegative(request.duration_ms),
    ttftMs: nonNegative(request.ttft_ms),
    promptMs,
    promptPerTokenMs,
    generationMs,
    generationPerTokenMs,
    cacheTokens,
    cachePercent: percent(cacheTokens, promptCacheTotal),
    draftProposed,
    draftAccepted,
    draftAcceptancePercent: percent(draftAccepted, draftProposed),
    contextUsed,
    contextMax: normalizedContextMax,
    contextPercent: percent(contextUsed, normalizedContextMax),
    finishReason: trimmed(inference.finish_reason) ?? trimmed(fallbackFinishReason),
    toolCallCount: nonNegative(inference.tool_call_count),
    wallEstimated: false,
    ttftEstimated: false
  }
}

function responseObject(value: Record<string, any>): Record<string, any> {
  return value.response && typeof value.response === 'object' && !Array.isArray(value.response)
    ? value.response
    : value
}

export function playgroundTurnStatsFromResponsePayload(payload: unknown, contextMax?: number): PlaygroundTurnStats | undefined {
  if (!payload || typeof payload !== 'object' || Array.isArray(payload)) return undefined
  const object = responseObject(payload as Record<string, any>)
  const usage = object.usage && typeof object.usage === 'object' && !Array.isArray(object.usage) ? object.usage : {}
  const timings = object.timings && typeof object.timings === 'object' && !Array.isArray(object.timings) ? object.timings : {}
  const choices = Array.isArray(object.choices) ? object.choices : []
  const finishReason = choices.map(choice => trimmed(choice?.finish_reason)).find(Boolean)
  const inference: InferenceTurnStats = {}
  for (const [source, target] of [
    ['prompt_n', 'prompt_n'], ['prompt_ms', 'prompt_ms'], ['prompt_per_second', 'prompt_per_second'], ['prompt_per_token_ms', 'prompt_per_token_ms'],
    ['predicted_n', 'predicted_n'], ['predicted_ms', 'predicted_ms'], ['predicted_per_second', 'predicted_per_second'], ['predicted_per_token_ms', 'predicted_per_token_ms'],
    ['cache_n', 'cache_n'], ['draft_n', 'draft_n'], ['draft_n_accepted', 'draft_n_accepted']
  ] as const) {
    const value = nonNegative(timings[source])
    if (value !== undefined) inference[target] = value
  }
  if (finishReason) inference.finish_reason = finishReason

  const request: PlaygroundDiagnosticRequest = {
    prompt_tokens: nonNegative(usage.prompt_tokens ?? usage.input_tokens),
    generated_tokens: nonNegative(usage.completion_tokens ?? usage.output_tokens),
    total_tokens: nonNegative(usage.total_tokens)
  }
  const stats = playgroundTurnStatsFromDiagnostics({ request, inference_stats: inference }, contextMax, finishReason)
  delete stats.wallEstimated
  delete stats.ttftEstimated
  const hasValue = Object.values(stats).some(value => value !== undefined)
  return hasValue ? stats : undefined
}

export function parsePlaygroundSSETurnStats(line: string, contextMax?: number): PlaygroundTurnStats | undefined {
  const trimmedLine = line.trim()
  if (!trimmedLine.startsWith('data:')) return undefined
  const data = trimmedLine.slice(5).trim()
  if (!data || data === '[DONE]') return undefined
  try {
    return playgroundTurnStatsFromResponsePayload(JSON.parse(data), contextMax)
  } catch {
    return undefined
  }
}

export function livePlaygroundTurnStats(startedAtMs: number, firstTokenAtMs: number | undefined, nowMs = Date.now()): PlaygroundTurnStats {
  const wallMs = Math.max(0, nowMs - startedAtMs)
  const ttftMs = firstTokenAtMs === undefined ? undefined : Math.max(0, firstTokenAtMs - startedAtMs)
  return {
    wallMs,
    wallEstimated: true,
    ttftMs,
    ttftEstimated: ttftMs !== undefined
  }
}
