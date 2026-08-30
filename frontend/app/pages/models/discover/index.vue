<script setup lang="ts">
import ModelsDiscover from '~/components/ModelsDiscover.vue'

const query = useState<string>('models-discover-query', () => '')
let normalizingRepositoryURL = false

function huggingFaceRepo(value: string) {
  let raw = value.trim()
  if (!raw) return ''
  if (/^(?:www\.)?huggingface\.co\//i.test(raw)) raw = `https://${raw}`
  if (!/^https?:\/\//i.test(raw)) return ''

  try {
    const url = new URL(raw)
    const host = url.hostname.toLowerCase().replace(/^www\./, '')
    if (host !== 'huggingface.co') return ''
    let parts = url.pathname.split('/').filter(Boolean)
    if (parts[0]?.toLowerCase() === 'models') parts = parts.slice(1)
    if (parts.length < 2) return ''
    if (['datasets', 'spaces'].includes(parts[0].toLowerCase())) return ''
    return `${parts[0]}/${parts[1]}`
  } catch {
    return ''
  }
}

watch(query, (value) => {
  if (normalizingRepositoryURL) {
    normalizingRepositoryURL = false
    return
  }

  const repo = huggingFaceRepo(value)
  if (!repo) return

  normalizingRepositoryURL = true
  query.value = repo
  const [owner, name] = repo.split('/', 2)
  if (!owner || !name) return
  void navigateTo(`/models/discover/${encodeURIComponent(owner)}/${encodeURIComponent(name)}`)
}, { flush: 'sync' })
</script>

<template>
  <ModelsDiscover key="discover-list" />
</template>
