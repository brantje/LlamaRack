<script setup lang="ts">
import ModelDeleteModal from '~/components/ModelDeleteModal.vue'

const manager = useManager()
const { models } = manager
const message = ref('')
const messageTitle = ref('')
const pending = ref<string | null>(null)

function clearMessage() {
  message.value = ''
  messageTitle.value = ''
}

function showCopyError(value: string) {
  messageTitle.value = 'Unable to copy model path'
  message.value = value
}
const deleteModal = ref<{
  request: (options: { name: string, path: string, sizeLabel?: string }) => Promise<{ confirmed: boolean, deleteFiles: boolean }>
} | null>(null)

function formatBytes(value: number) {
  if (!value) return '0 B'
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB']
  let size = value
  let unit = 0
  while (size >= 1024 && unit < units.length - 1) {
    size /= 1024
    unit++
  }
  return `${size >= 10 || unit === 0 ? size.toFixed(0) : size.toFixed(1)} ${units[unit]}`
}

function contextLabel(value: number) {
  return value > 0 ? value.toLocaleString() : 'Unknown'
}

async function remove(id: string) {
  const model = models.value.find(item => item.id === id)
  if (!model) return
  const result = await deleteModal.value?.request({
    name: model.name,
    path: model.gguf_path,
    sizeLabel: model.total_bytes > 0 ? formatBytes(model.total_bytes) : undefined
  })
  if (!result?.confirmed) return
  pending.value = id
  clearMessage()
  try {
    const suffix = result.deleteFiles ? '?delete_files=true' : ''
    await manager.request(`/api/v1/models/${encodeURIComponent(model.slug)}${suffix}`, { method: 'DELETE' })
    await manager.refresh()
  } catch (error: any) {
    messageTitle.value = 'Unable to delete model'
    message.value = error?.data?.error || error?.message || 'Unable to delete model'
  } finally {
    pending.value = null
  }
}
</script>

<template>
  <div class="space-y-5">
    <div class="flex flex-wrap items-start justify-between gap-5">
      <UPageHeader
        class="min-w-0 flex-1"
        headline="MODEL REGISTRY"
        title="Models"
        description="Registered GGUF inventory and reusable llama.cpp defaults. Runtime lifecycle is managed from Instances."
      />
      <div class="flex w-full flex-wrap justify-start gap-2 sm:w-auto sm:justify-end">
        <AppButton type="button" intent="secondary" @click="manager.refresh">Refresh</AppButton>
        <AppButton to="/models/discover" intent="secondary" icon="i-lucide-search">Discover</AppButton>
        <AppButton to="/models/new" intent="primary">Add model</AppButton>
      </div>
    </div>

    <Frame v-if="message" class="border-[var(--accent-800)] p-3" data-testid="models-error-state">
      <p class="text-sm font-semibold text-[var(--accent-900)]">{{ messageTitle || 'Model operation failed' }}</p>
      <p class="mt-1 text-xs text-[var(--neutral-800)]">{{ message }}</p>
    </Frame>

    <Frame v-if="!models.length" class="p-8 text-center" data-testid="models-empty-state">
      <h2 class="text-base font-semibold">No models registered</h2>
      <p class="mt-2 text-sm text-[var(--neutral-700)]">Register a local GGUF file to get started.</p>
      <div class="mt-4 flex justify-center">
        <AppButton to="/models/new" intent="primary" size="sm">Add model</AppButton>
      </div>
    </Frame>

    <Frame v-else class="overflow-hidden p-0">
      <div class="overflow-x-auto" role="region" aria-label="Registered models table. Scroll horizontally to view all columns on small screens." tabindex="0" data-testid="models-table-scroll">
        <p class="border-b border-[var(--color-divider)] px-4 py-2 text-xs text-[var(--neutral-700)] md:hidden">Scroll horizontally for path, context and actions.</p>
        <table class="min-w-[920px] w-full border-collapse text-left" data-testid="models-table">
          <thead class="border-b border-[var(--color-divider)] bg-[var(--neutral-100)] text-[length:var(--font-size-table-header)] uppercase tracking-[.08em] text-[var(--neutral-700)]">
            <tr>
              <th class="px-4 py-3 font-semibold">Name</th>
              <th class="px-4 py-3 font-semibold">Path</th>
              <th class="px-4 py-3 font-semibold">Size</th>
              <th class="px-4 py-3 font-semibold">Quantization</th>
              <th class="px-4 py-3 font-semibold">Context capability</th>
              <th class="px-4 py-3 text-right font-semibold">Actions</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-[var(--color-divider)]">
            <tr v-for="model in models" :key="model.id" data-testid="model-row">
              <td class="px-4 py-3">
                <NuxtLink :to="`/models/${encodeURIComponent(model.slug)}/details`" class="text-[length:var(--font-size-table-body)] font-semibold text-[var(--color-text)] hover:underline">
                  {{ model.name }}
                </NuxtLink>
              </td>
              <td class="min-w-[260px] max-w-[420px] break-words px-4 py-3 font-mono text-[length:var(--font-size-h6)] text-[var(--neutral-700)]">
                <div class="flex min-w-0 items-start gap-1">
                  <span class="min-w-0 flex-1">{{ model.gguf_path }}</span>
                  <AppCopyButton
                    :text="model.gguf_path"
                    label="Copy model path"
                    copied-label="Copied model path"
                    error-message="Unable to copy model path. Select the path and copy it manually."
                    icon-only
                    color="neutral"
                    variant="ghost"
                    size="xs"
                    :data-testid="`copy-model-path-${model.id}`"
                    @copied="clearMessage"
                    @error="showCopyError"
                  />
                </div>
              </td>
              <td class="whitespace-nowrap px-4 py-3 text-sm">{{ formatBytes(model.total_bytes) }}</td>
              <td class="whitespace-nowrap px-4 py-3 text-sm">{{ model.quantization || '—' }}</td>
              <td class="whitespace-nowrap px-4 py-3 text-sm">{{ contextLabel(model.context_length) }}</td>
              <td class="whitespace-nowrap px-4 py-3">
                <div class="flex justify-end gap-1">
                  <AppButton :to="`/models/${encodeURIComponent(model.slug)}/details`" intent="ghost" size="xs">Details</AppButton>
                  <AppButton :to="`/models/${encodeURIComponent(model.slug)}/edit`" intent="ghost" size="xs">Edit</AppButton>
                  <AppButton type="button" intent="ghost" size="xs" :loading="pending === model.id" @click="remove(model.id)">Delete</AppButton>
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </Frame>

    <ModelDeleteModal ref="deleteModal" />
  </div>
</template>