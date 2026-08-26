<script setup lang="ts">
defineOptions({ inheritAttrs: false })

const props = withDefaults(defineProps<{
  text: string
  label?: string
  copiedLabel?: string
  errorMessage?: string
  iconOnly?: boolean
}>(), {
  label: 'Copy',
  copiedLabel: 'Copied',
  errorMessage: 'Unable to copy. Select the value and copy it manually.',
  iconOnly: false
})

const emit = defineEmits<{
  copied: [text: string]
  error: [message: string]
}>()

const copied = ref(false)
let copiedResetTimer: ReturnType<typeof setTimeout> | undefined

function resetCopiedState() {
  clearTimeout(copiedResetTimer)
  copiedResetTimer = undefined
  copied.value = false
}

function markCopied() {
  clearTimeout(copiedResetTimer)
  copied.value = true
  emit('copied', props.text)
  copiedResetTimer = setTimeout(() => {
    copied.value = false
    copiedResetTimer = undefined
  }, 5000)
}

watch(() => props.text, resetCopiedState)
onBeforeUnmount(() => clearTimeout(copiedResetTimer))

function legacyCopy(text: string) {
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

async function copy() {
  resetCopiedState()
  if (!props.text) return

  let clipboardError = ''

  if (typeof navigator !== 'undefined') {
    try {
      if (navigator.clipboard?.writeText) {
        try {
          await navigator.clipboard.writeText(props.text)
          markCopied()
          return
        } catch (value: any) {
          clipboardError = value?.message || ''
          // Firefox can expose Clipboard while rejecting it outside a secure
          // context. Continue with the selection-based fallback.
        }
      }
    } catch {
      // Some browser/privacy configurations can throw while resolving the
      // clipboard capability itself. Continue with the fallback.
    }
  }

  if (legacyCopy(props.text)) {
    markCopied()
    return
  }

  emit('error', clipboardError ? `${clipboardError}. ${props.errorMessage}` : props.errorMessage)
}

const accessibleLabel = computed(() => {
  const action = copied.value ? props.copiedLabel : props.label
  return props.iconOnly ? `${action} ${props.text}` : action
})
</script>

<template>
  <UButton
    v-bind="$attrs"
    :icon="copied ? 'i-lucide-check' : 'i-lucide-copy'"
    :aria-label="accessibleLabel"
    :title="copied ? copiedLabel : label"
    @click="copy"
  >
    <template v-if="!iconOnly">{{ label }}</template>
  </UButton>
</template>
