import { describe, expect, it } from 'vitest'
import {
  PLAYGROUND_EMPTY_CONTENT_FALLBACK,
  PLAYGROUND_REASONING_ONLY_FALLBACK,
  extractChatDelta,
  isLengthFinishReason,
  parseSSEDataLine,
  playgroundEmptyContentFallback
} from '~/utils/playgroundChatStream'

describe('playgroundChatStream', () => {
  it('extracts text, reasoning and finish_reason from OpenAI-compatible choices', () => {
    expect(extractChatDelta(null)).toEqual({})
    expect(extractChatDelta('x')).toEqual({})
    expect(extractChatDelta({ delta: { content: 'Hello' } })).toEqual({ text: 'Hello' })
    expect(extractChatDelta({ delta: { content: '', reasoning_content: 'think' } })).toEqual({ reasoning: 'think' })
    expect(extractChatDelta({ message: { content: 'done', reasoning: 'why' } })).toEqual({ text: 'done', reasoning: 'why' })
    expect(extractChatDelta({ text: 'legacy' })).toEqual({ text: 'legacy' })
    expect(extractChatDelta({ delta: {}, message: { content: 'from message' } })).toEqual({ text: 'from message' })
    expect(extractChatDelta({ delta: { thinking: 'plan', content: [{ type: 'text', text: 'Hi' }, { type: 'text', text: ' there' }] } })).toEqual({
      text: 'Hi there',
      reasoning: 'plan'
    })
    expect(extractChatDelta({ delta: { content: 7 } })).toEqual({})
    expect(extractChatDelta({ delta: { content: [] } })).toEqual({})
    expect(extractChatDelta({ finish_reason: 'length', delta: { content: 'cut' } })).toEqual({ text: 'cut', finishReason: 'length' })
    expect(extractChatDelta({ delta: { finish_reason: 'stop' } })).toEqual({ finishReason: 'stop' })
    expect(extractChatDelta({ message: { finish_reason: 'length', reasoning_content: 'plan' } })).toEqual({
      reasoning: 'plan',
      finishReason: 'length'
    })
  })

  it('parses SSE data lines and ignores keepalive, done and malformed frames', () => {
    expect(parseSSEDataLine(': keepalive')).toBeNull()
    expect(parseSSEDataLine('data:')).toBeNull()
    expect(parseSSEDataLine('data: [DONE]')).toBeNull()
    expect(parseSSEDataLine('data: {bad json')).toBeNull()
    expect(parseSSEDataLine('data: {"choices":[{"delta":{"content":"Hi"}}]}')).toEqual({ text: 'Hi' })
    expect(parseSSEDataLine('data: {"choices":[{"delta":{"reasoning_content":"hmm"},"finish_reason":"length"}]}')).toEqual({
      reasoning: 'hmm',
      finishReason: 'length'
    })
  })

  it('selects empty-content fallback copy and detects length truncation', () => {
    expect(playgroundEmptyContentFallback(true)).toBe(PLAYGROUND_REASONING_ONLY_FALLBACK)
    expect(playgroundEmptyContentFallback(false)).toBe(PLAYGROUND_EMPTY_CONTENT_FALLBACK)
    expect(isLengthFinishReason('length')).toBe(true)
    expect(isLengthFinishReason('stop')).toBe(false)
    expect(isLengthFinishReason(undefined)).toBe(false)
  })
})
