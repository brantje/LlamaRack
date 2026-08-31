export const PLAYGROUND_ATTACHMENT_ACCEPT = 'image/*'
export const PLAYGROUND_MAX_ATTACHMENT_BYTES = 8 * 1024 * 1024
export const PLAYGROUND_MAX_ATTACHMENTS = 4

export type PlaygroundFilePart = {
  type: 'file'
  url: string
  mediaType: string
  filename: string
}

export type PlaygroundTextPart = {
  type: 'text'
  text: string
}

export type PlaygroundParsedPart = PlaygroundTextPart | PlaygroundFilePart

export function isPlaygroundImageType(mediaType: string) {
  return mediaType.startsWith('image/')
}

export function readFileAsDataUrl(file: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => resolve(String(reader.result || ''))
    reader.onerror = () => reject(reader.error || new Error('Unable to read file.'))
    reader.readAsDataURL(file)
  })
}

export function buildApiMessageContent(text: string, files: Array<{ dataUrl: string, mediaType: string }>) {
  const images = files.filter(file => isPlaygroundImageType(file.mediaType))
  if (!images.length) return text
  const items: Array<Record<string, unknown>> = []
  const trimmed = text.trim()
  if (trimmed) items.push({ type: 'text', text: trimmed })
  for (const file of images) {
    items.push({ type: 'image_url', image_url: { url: file.dataUrl } })
  }
  return items
}

export function parseApiMessageContent(content: unknown): PlaygroundParsedPart[] {
  if (typeof content === 'string') return content ? [{ type: 'text', text: content }] : []
  if (!Array.isArray(content)) return []
  const parts: PlaygroundParsedPart[] = []
  for (const item of content) {
    if (item?.type === 'text' && typeof item.text === 'string') {
      parts.push({ type: 'text', text: item.text })
      continue
    }
    if (item?.type === 'image_url') {
      const url = typeof item.image_url?.url === 'string'
        ? item.image_url.url
        : typeof item.url === 'string'
          ? item.url
          : ''
      if (url) parts.push({ type: 'file', url, mediaType: 'image/*', filename: 'image' })
    }
  }
  return parts
}

export function threadPartsToApiContent(parts: Array<{ type: string, text?: string, url?: string, mediaType?: string, filename?: string }>) {
  const text = parts.filter(part => part.type === 'text').map(part => part.text || '').join('')
  const files = parts
    .filter(part => part.type === 'file' && part.url && part.mediaType)
    .map(part => ({ dataUrl: String(part.url), mediaType: String(part.mediaType) }))
  return buildApiMessageContent(text, files)
}
