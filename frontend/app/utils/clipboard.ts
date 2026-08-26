export async function copyText(text: string) {
  if (!text) return false

  if (typeof navigator !== 'undefined') {
    try {
      if (navigator.clipboard?.writeText) {
        try {
          await navigator.clipboard.writeText(text)
          return true
        } catch {
          // Firefox may expose the Clipboard API while still denying it in an
          // insecure context. Fall through to the selection-based fallback.
        }
      }
    } catch {
      // Some environments can throw while resolving clipboard capabilities.
    }
  }

  if (typeof document === 'undefined') return false

  const textarea = document.createElement('textarea')
  textarea.value = text
  textarea.setAttribute('readonly', '')
  textarea.style.position = 'fixed'
  textarea.style.left = '-9999px'
  textarea.style.top = '0'
  textarea.style.opacity = '0'
  document.body.appendChild(textarea)

  try {
    textarea.focus()
    textarea.select()
    textarea.setSelectionRange(0, textarea.value.length)
    return document.execCommand?.('copy') === true
  } catch {
    return false
  } finally {
    textarea.remove()
  }
}
