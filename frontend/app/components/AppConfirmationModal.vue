<script setup lang="ts">
type ConfirmationOptions = {
  title: string
  description: string
  confirmLabel?: string
  cancelLabel?: string
  color?: 'primary' | 'error' | 'warning'
}

const open = ref(false)
const dialog = reactive({
  title: '',
  description: '',
  confirmLabel: 'Confirm',
  cancelLabel: 'Cancel',
  color: 'primary' as 'primary' | 'error' | 'warning'
})
let resolver: ((confirmed: boolean) => void) | null = null

function finish(confirmed: boolean) {
  const resolve = resolver
  resolver = null
  open.value = false
  resolve?.(confirmed)
}

function request(options: ConfirmationOptions) {
  if (resolver) finish(false)
  Object.assign(dialog, {
    title: options.title,
    description: options.description,
    confirmLabel: options.confirmLabel || 'Confirm',
    cancelLabel: options.cancelLabel || 'Cancel',
    color: options.color || 'primary'
  })
  open.value = true
  return new Promise<boolean>((resolve) => {
    resolver = resolve
  })
}

watch(open, (value) => {
  if (!value && resolver) finish(false)
})

defineExpose({ request })
</script>

<template>
  <UModal v-model:open="open" :title="dialog.title">
    <template #body>
      <p class="text-sm leading-6 text-muted">{{ dialog.description }}</p>
    </template>
    <template #footer>
      <div class="flex w-full justify-end gap-2">
        <UButton data-testid="confirmation-cancel" color="neutral" variant="soft" @click="finish(false)">
          {{ dialog.cancelLabel }}
        </UButton>
        <UButton data-testid="confirmation-confirm" :color="dialog.color" @click="finish(true)">
          {{ dialog.confirmLabel }}
        </UButton>
      </div>
    </template>
  </UModal>
</template>
