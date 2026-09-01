<script setup lang="ts">
type CreateResponse = { model: { id: string }; instance?: { id: string }; start_error?: string }

const manager = useManager()
const router = useRouter()
const route = useRoute()
const busy = ref(false)
const error = ref('')
const createFirstInstance = ref(true)
const remoteRepo = computed(() => typeof route.query.repo === 'string' ? route.query.repo.trim() : '')
const remoteArtifactID = computed(() => typeof route.query.artifact === 'string' ? route.query.artifact.trim() : '')
const remoteMode = computed(() => Boolean(remoteRepo.value && remoteArtifactID.value))
const form = reactive({
  gguf_path: '',
  name: '',
  context_length: 0,
  options: {} as Record<string, string>,
  first_instance: {
    name: '',
    slug: '',
    always_on: false,
    autoload_enabled: true,
    eviction_enabled: true,
    start: false
  }
})

async function createModel() {
  busy.value = true
  error.value = ''
  try {
    if (remoteMode.value) {
      const result = await manager.request<CreateResponse>('/api/v1/huggingface/import', {
        method: 'POST',
        body: {
          repo_id: remoteRepo.value,
          artifact_id: remoteArtifactID.value,
          name: form.name,
          context_length: form.context_length,
          options: form.options,
          first_instance: form.first_instance
        }
      })
      await manager.refresh()
      await router.push('/instances')
      return result
    }

    const body = {
      gguf_path: form.gguf_path,
      name: form.name,
      context_length: form.context_length,
      options: form.options,
      first_instance: createFirstInstance.value ? form.first_instance : undefined
    }
    const result = await manager.request<CreateResponse>('/api/v1/models', { method: 'POST', body })
    await manager.refresh()
    if (result.start_error) {
      error.value = `Model and Instance were created, but llama-server failed to start: ${result.start_error}`
      return
    }
    await router.push(createFirstInstance.value ? '/instances' : '/models')
  } catch (e: any) {
    error.value = e?.data?.error || e?.message || (remoteMode.value ? 'Unable to create downloading Instance' : 'Unable to create model')
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <ModelForm
    :form="form"
    mode="create"
    :title="remoteMode ? 'Launch Hugging Face model' : 'Add model'"
    :description="remoteMode ? 'Configure the Model and its first Instance now. The Instance stays in Downloading state until the selected GGUF is ready.' : 'Register a GGUF model and optionally bootstrap its first addressable Instance.'"
    :submit-label="remoteMode ? 'Create and download' : 'Create model'"
    :busy="busy"
    :error="error"
    :remote="remoteMode"
    :remote-repo="remoteRepo"
    :remote-artifact-id="remoteArtifactID"
    :back-to="remoteMode ? '/models/discover' : '/models'"
    :back-label="remoteMode ? 'Back to Discover' : 'Back to models'"
    v-model:create-first-instance="createFirstInstance"
    @submit="createModel"
  />
</template>
