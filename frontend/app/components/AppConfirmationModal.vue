<script setup lang="ts">
type ConfirmationOptions = {
  title: string
  description: string
  confirmLabel?: string
  cancelLabel?: string
  confirmIntent?: 'primary' | 'secondary' | 'ghost'
  confirmTone?: 'default' | 'destructive'
}

const open = ref(false)
const dialog = reactive({
  title: '',
  description: '',
  confirmLabel: 'Confirm',
  cancelLabel: 'Cancel',
  confirmIntent: 'primary' as 'primary' | 'secondary' | 'ghost',
  confirmTone: 'default' as 'default' | 'destructive'
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
    confirmIntent: options.confirmIntent || 'primary',
    confirmTone: options.confirmTone || 'default'
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
        <AppButton data-testid="confirmation-cancel" intent="secondary" @click="finish(false)">
          {{ dialog.cancelLabel }}
        </AppButton>
        <AppButton data-testid="confirmation-confirm" :intent="dialog.confirmIntent" :tone="dialog.confirmTone" @click="finish(true)">
          {{ dialog.confirmLabel }}
        </AppButton>
      </div>
    </template>
  </UModal>
</template>
