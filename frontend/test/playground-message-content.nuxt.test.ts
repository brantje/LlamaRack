import { describe, expect, it, vi } from 'vitest'
import {
  PLAYGROUND_MAX_ATTACHMENTS,
  buildApiMessageContent,
  isPlaygroundImageType,
  parseApiMessageContent,
  readFileAsDataUrl,
  threadPartsToApiContent
} from '~/utils/playgroundMessageContent'

describe('playgroundMessageContent', () => {
  it('builds plain text and multimodal OpenAI-compatible content', () => {
    expect(buildApiMessageContent('hello', [])).toBe('hello')
    expect(buildApiMessageContent('', [{ dataUrl: 'data:image/png;base64,abc', mediaType: 'image/png' }])).toEqual([
      { type: 'image_url', image_url: { url: 'data:image/png;base64,abc' } }
    ])
    expect(buildApiMessageContent('describe this', [{ dataUrl: 'data:image/jpeg;base64,xyz', mediaType: 'image/jpeg' }])).toEqual([
      { type: 'text', text: 'describe this' },
      { type: 'image_url', image_url: { url: 'data:image/jpeg;base64,xyz' } }
    ])
    expect(buildApiMessageContent('text only', [{ dataUrl: 'data:application/pdf;base64,abc', mediaType: 'application/pdf' }])).toBe('text only')
  })

  it('parses string and image_url message content back into thread parts', () => {
    expect(parseApiMessageContent('')).toEqual([])
    expect(parseApiMessageContent('hello')).toEqual([{ type: 'text', text: 'hello' }])
    expect(parseApiMessageContent(null)).toEqual([])
    expect(parseApiMessageContent([
      { type: 'text', text: 'look' },
      { type: 'image_url', image_url: { url: 'data:image/png;base64,abc' } },
      { type: 'image_url', url: 'data:image/jpeg;base64,xyz' },
      { type: 'image_url', image_url: {} }
    ])).toEqual([
      { type: 'text', text: 'look' },
      { type: 'file', url: 'data:image/png;base64,abc', mediaType: 'image/*', filename: 'image' },
      { type: 'file', url: 'data:image/jpeg;base64,xyz', mediaType: 'image/*', filename: 'image' }
    ])
  })

  it('round-trips thread parts to API content', () => {
    const parts = [
      { type: 'file', url: 'data:image/png;base64,abc', mediaType: 'image/png', filename: 'diagram.png' },
      { type: 'text', text: 'What is this?' }
    ] as const
    expect(threadPartsToApiContent([...parts])).toEqual([
      { type: 'text', text: 'What is this?' },
      { type: 'image_url', image_url: { url: 'data:image/png;base64,abc' } }
    ])
    expect(threadPartsToApiContent([{ type: 'file', url: '', mediaType: 'image/png', filename: 'broken.png' }])).toBe('')
  })

  it('accepts only image media types for attachments', () => {
    expect(isPlaygroundImageType('image/png')).toBe(true)
    expect(isPlaygroundImageType('application/pdf')).toBe(false)
    expect(PLAYGROUND_MAX_ATTACHMENTS).toBeGreaterThan(0)
  })

  it('reads files as data URLs and surfaces read failures', async () => {
    vi.stubGlobal('FileReader', class {
      result: string | ArrayBuffer | null = 'data:image/png;base64,abc'
      onload: ((this: FileReader, ev: ProgressEvent<FileReader>) => any) | null = null
      onerror: ((this: FileReader, ev: ProgressEvent<FileReader>) => any) | null = null
      readAsDataURL() {
        this.onload?.call(this, {} as ProgressEvent<FileReader>)
      }
    })
    await expect(readFileAsDataUrl(new Blob(['x']))).resolves.toBe('data:image/png;base64,abc')

    vi.stubGlobal('FileReader', class {
      result: string | ArrayBuffer | null = ''
      onload: ((this: FileReader, ev: ProgressEvent<FileReader>) => any) | null = null
      onerror: ((this: FileReader, ev: ProgressEvent<FileReader>) => any) | null = null
      readAsDataURL() {
        this.onload?.call(this, {} as ProgressEvent<FileReader>)
      }
    })
    await expect(readFileAsDataUrl(new Blob(['x']))).resolves.toBe('')

    vi.stubGlobal('FileReader', class {
      onload: ((this: FileReader, ev: ProgressEvent<FileReader>) => any) | null = null
      onerror: ((this: FileReader, ev: ProgressEvent<FileReader>) => any) | null = null
      error = new Error('broken reader')
      readAsDataURL() {
        this.onerror?.call(this, {} as ProgressEvent<FileReader>)
      }
    })
    await expect(readFileAsDataUrl(new Blob(['x']))).rejects.toThrow('broken reader')
  })
})
