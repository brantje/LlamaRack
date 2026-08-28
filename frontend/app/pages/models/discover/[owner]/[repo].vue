<script setup lang="ts">
import ModelsDiscover from '~/components/ModelsDiscover.vue'

const route = useRoute()
const repoID = computed(() => `${String(route.params.owner || '')}/${String(route.params.repo || '')}`)
</script>

<template>
  <ModelsDiscover :key="`discover-detail:${repoID}`" :repo-id="repoID" />
</template>
