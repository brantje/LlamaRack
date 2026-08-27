<script setup lang="ts">
type ModelDeleteOptions = {
  name: string
  path: string
  sizeLabel?: string
}

type ModelDeleteResult = {
  confirmed: boolean
  deleteFiles: boolean
}

const open = ref(false)
const deleteFiles = ref(false)
const dialog = reactive<ModelDeleteOptions>({ name: '', path: '', sizeLabel: '' })
let resolver: ((result: ModelDeleteResult) => void) | null = null

function finish(confirmed: boolean) {
  const resolve = resolver
  const result = { confirmed, deleteFiles: confirmed && deleteFiles.value }
  resolver = null
  open.value = false
  resolve?.(result)
}

function request(options: ModelDeleteOptions) {
  if (resolver) finish(false)
  Object.assign(dialog, options)
  deleteFiles.value = false
  open.value = true
  return new Promise<ModelDeleteResult>((resolve) => {
    resolver = resolve
  })
}

watch(open, (value) => {
  if (!value && resolver) finish(false)
})

defineExpose({ request })
</script>

<template>
  <UModal v-model:open="open" title="Delete Model">
    <template #body>
      <div class="space-y-4">
        <p class="text-sm leading-6 text-muted">
          Delete registered Model “{{ dialog.name }}”? Its Instance definitions will also be deleted. The model file will be preserved unless you explicitly choose to remove it below.
        </p>

        <UCheckbox
          v-model="deleteFiles"
          data-testid="model-delete-files"
          label="Also delete model files from disk"
        />

        <UAlert
          v-if="deleteFiles"
          data-testid="model-delete-warning"
          color="error"
          variant="subtle"
          title="Permanent file deletion"
          description="The registered Model and its Instance definitions will be deleted. The associated model file(s) will also be permanently removed from disk. This cannot be undone."
        />

        <dl v-if="deleteFiles" class="grid gap-2 rounded-lg border border-error/30 bg-error/5 p-3 text-sm">
          <div class="grid gap-1">
            <dt class="font-medium text-highlighted">Backing artifact</dt>
            <dd class="break-all font-mono text-xs text-muted">{{ dialog.path }}</dd>
          </div>
          <div v-if="dialog.sizeLabel" class="grid gap-1">
            <dt class="font-medium text-highlighted">Artifact size</dt>
            <dd class="text-muted">{{ dialog.sizeLabel }}</dd>
          </div>
        </dl>
      </div>
    </template>

    <template #footer>
      <div class="flex w-full justify-end gap-2">
        <UButton data-testid="confirmation-cancel" color="neutral" variant="soft" @click="finish(false)">
          Cancel
        </UButton>
        <UButton data-testid="confirmation-confirm" color="error" @click="finish(true)">
          {{ deleteFiles ? 'Delete Model and Files' : 'Delete Model' }}
        </UButton>
      </div>
    </template>
  </UModal>
</template>
