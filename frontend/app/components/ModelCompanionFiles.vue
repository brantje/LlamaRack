<script setup lang="ts">
import {
  companionDefinitions,
  nativeMTPDefaults,
  nativeMTPOptionKeys,
  isNativeMTP,
  type CompanionDefinition,
  type CompanionDependency,
  type ModelInspection
} from '~/utils/modelCompanions'

type RemoteArtifact = { dependencies?: CompanionDependency[] }

const props = withDefaults(defineProps<{
  modelValue: Record<string, string>
  inspection?: ModelInspection | null
  fallbackSuggestedOptions?: Record<string, string>
  remote?: boolean
  remoteArtifact?: RemoteArtifact | null
  inspecting?: boolean
  testid?: string
  description?: string
}>(), {
  inspection: null,
  fallbackSuggestedOptions: () => ({}),
  remote: false,
  remoteArtifact: null,
  inspecting: false,
  testid: 'detected-gguf-helpers',
  description: 'Scanned alongside the selected GGUF · options filled automatically.'
})

const emit = defineEmits<{ 'update:modelValue': [value: Record<string, string>] }>()

function filename(value: string) {
  return value.split(/[\\/]/).pop() || value
}

function formatBytes(value: number) {
  if (!value) return 'Unknown size'
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB']
  let amount = value
  let index = 0
  while (amount >= 1024 && index < units.length - 1) { amount /= 1024; index++ }
  return `${amount >= 10 || index === 0 ? amount.toFixed(0) : amount.toFixed(1)} ${units[index]}`
}

function setOptions(next: Record<string, string>) {
  emit('update:modelValue', next)
}

function dependencyFor(definition: CompanionDefinition) {
  const dependencies = props.remote ? (props.remoteArtifact?.dependencies || []) : (props.inspection?.dependencies || [])
  return dependencies.find(dependency => dependency.kind === definition.kind) || null
}

function candidateList(definition: CompanionDefinition) {
  if (props.remote) return []
  const candidates = (props.inspection?.dependency_candidates || []).filter(candidate => candidate.kind === definition.kind)
  if (candidates.length) return candidates
  const selected = dependencyFor(definition)
  const optionPath = props.inspection?.suggested_options?.[definition.key] || props.fallbackSuggestedOptions[definition.key]
  if (!selected || !optionPath) return []
  return [{ ...selected, option_path: optionPath }]
}

function detectedCompanionPath(definition: CompanionDefinition) {
  if (props.remote) return ''
  return props.inspection?.suggested_options?.[definition.key]
    || props.fallbackSuggestedOptions[definition.key]
    || dependencyFor(definition)?.option_path
    || ''
}

function nativeMTP() {
  if (props.remote) return false
  return isNativeMTP(props.modelValue, props.inspection, props.fallbackSuggestedOptions)
}

function nativeMTPCompanion(definition: CompanionDefinition) {
  return definition.kind === 'mtp' && nativeMTP()
}

function companionTitle(definition: CompanionDefinition) {
  return nativeMTPCompanion(definition) ? 'Built-in MTP' : definition.title
}

function companionFlag(definition: CompanionDefinition) {
  return nativeMTPCompanion(definition) ? '--spec-type' : definition.flag
}

function companionStatusLabel(definition: CompanionDefinition, state: 'detected' | 'disabled' | 'none') {
  if (state === 'detected') return nativeMTPCompanion(definition) ? 'Built-in' : 'Auto-detected'
  if (state === 'disabled') return 'Ignored'
  return 'None found'
}

function nativeMTPParamSummary() {
  return nativeMTPOptionKeys.map(key => `${key}=${props.modelValue[key] || nativeMTPDefaults[key]}`).join(' · ')
}

function companionAvailable(definition: CompanionDefinition) {
  if (nativeMTPCompanion(definition)) return true
  if (props.remote) return Boolean(dependencyFor(definition))
  if (props.modelValue[definition.key]) return true
  return Boolean(detectedCompanionPath(definition) || candidateList(definition).length)
}

function companionState(definition: CompanionDefinition): 'detected' | 'disabled' | 'none' {
  if (!companionAvailable(definition)) return 'none'
  if (nativeMTPCompanion(definition)) {
    if (Object.prototype.hasOwnProperty.call(props.modelValue, 'spec-type') && props.modelValue['spec-type'] === '') return 'disabled'
    return 'detected'
  }
  if (Object.prototype.hasOwnProperty.call(props.modelValue, definition.key) && props.modelValue[definition.key] === '') return 'disabled'
  return 'detected'
}

function companionValue(definition: CompanionDefinition) {
  if (Object.prototype.hasOwnProperty.call(props.modelValue, definition.key)) return props.modelValue[definition.key] || ''
  if (props.remote) return dependencyFor(definition)?.files?.[0]?.path || dependencyFor(definition)?.name || ''
  return detectedCompanionPath(definition)
}

function activeCandidate(definition: CompanionDefinition) {
  const value = companionValue(definition)
  return candidateList(definition).find(candidate => candidate.option_path === value) || null
}

function companionDisplayName(definition: CompanionDefinition) {
  return activeCandidate(definition)?.name
    || dependencyFor(definition)?.name
    || filename(companionValue(definition))
}

function companionSize(definition: CompanionDefinition) {
  return activeCandidate(definition)?.total_bytes || dependencyFor(definition)?.total_bytes || 0
}

function mtpSuggestedValue(key: (typeof nativeMTPOptionKeys)[number]) {
  return props.inspection?.suggested_options?.[key] || props.fallbackSuggestedOptions[key] || nativeMTPDefaults[key]
}

function disableCompanion(definition: CompanionDefinition) {
  const next = { ...props.modelValue }
  if (nativeMTPCompanion(definition)) {
    for (const key of nativeMTPOptionKeys) next[key] = ''
  } else {
    next[definition.key] = ''
    if (definition.kind === 'mtp') next['spec-type'] = ''
  }
  setOptions(next)
}

function enableCompanion(definition: CompanionDefinition) {
  const next = { ...props.modelValue }
  if (nativeMTPCompanion(definition)) {
    for (const key of nativeMTPOptionKeys) next[key] = mtpSuggestedValue(key)
    setOptions(next)
    return
  }
  if (props.remote) {
    delete next[definition.key]
    if (definition.kind === 'mtp') delete next['spec-type']
  } else {
    const selected = dependencyFor(definition)
    const candidate = candidateList(definition).find(item => item.name === selected?.name) || candidateList(definition)[0]
    const path = candidate?.option_path || detectedCompanionPath(definition)
    if (path) next[definition.key] = path
    else delete next[definition.key]
    if (definition.kind === 'mtp') next['spec-type'] = 'draft-mtp'
  }
  setOptions(next)
}

function chooseCompanionCandidate(definition: CompanionDefinition, candidate: CompanionDependency) {
  if (!candidate.option_path) return
  const next = { ...props.modelValue, [definition.key]: candidate.option_path }
  if (definition.kind === 'mtp') next['spec-type'] = 'draft-mtp'
  setOptions(next)
}

function setCompanionValue(definition: CompanionDefinition, value: unknown) {
  if (props.remote) return
  const next = { ...props.modelValue, [definition.key]: String(value || '') }
  if (definition.kind === 'mtp' && next[definition.key]) next['spec-type'] = 'draft-mtp'
  setOptions(next)
}
</script>

<template>
  <Frame id="model-companions" class="p-5 scroll-mt-4" :data-testid="testid">
    <div class="mb-4">
      <div class="flex flex-wrap items-center gap-2">
        <p class="text-[length:var(--font-size-kicker)] font-extrabold tracking-[0.18em] text-[var(--neutral-700)]">COMPANIONS</p>
        <span v-if="inspecting" class="text-[length:var(--font-size-kicker)] uppercase tracking-[.12em] text-[var(--neutral-700)]">Resolving</span>
      </div>
      <h2 class="mt-1 text-base font-semibold">Companion files</h2>
      <p class="mt-1 text-xs text-[var(--neutral-700)]">{{ description }}</p>
    </div>
    <div class="grid gap-4 lg:grid-cols-2">
      <div
        v-for="definition in companionDefinitions"
        :key="definition.key"
        class="border p-4"
        :class="companionState(definition) === 'detected' ? 'border-[var(--color-accent)]' : 'border-[var(--color-divider)]'"
        :data-testid="`companion-${definition.kind}`"
      >
        <div class="flex flex-wrap items-start justify-between gap-3">
          <div>
            <p class="font-semibold">{{ companionTitle(definition) }}</p>
            <p class="mt-1 font-mono text-[length:var(--font-size-table-header)] text-[var(--neutral-700)]">{{ companionFlag(definition) }}</p>
          </div>
          <div class="flex items-center gap-2">
            <StatusTag v-if="companionState(definition) === 'detected'" variant="ready">{{ companionStatusLabel(definition, 'detected') }}</StatusTag>
            <StatusTag v-else-if="companionState(definition) === 'disabled'" variant="neutral">{{ companionStatusLabel(definition, 'disabled') }}</StatusTag>
            <StatusTag v-else variant="neutral">{{ companionStatusLabel(definition, 'none') }}</StatusTag>
            <AppButton v-if="nativeMTPCompanion(definition) && companionState(definition) === 'detected'" type="button" intent="ghost" size="xs" @click="disableCompanion(definition)">Disable</AppButton>
            <AppButton v-else-if="nativeMTPCompanion(definition) && companionState(definition) === 'disabled'" type="button" intent="ghost" size="xs" @click="enableCompanion(definition)">Enable</AppButton>
          </div>
        </div>

        <template v-if="companionState(definition) === 'detected' && nativeMTPCompanion(definition)">
          <p class="mt-4 text-[length:var(--font-size-table-header)] text-[var(--neutral-700)]" data-testid="companion-native-mtp">Packed into this GGUF · speculative decoding defaults applied automatically<span v-if="inspection?.features?.nextn_predict_layers"> · nextn_predict_layers {{ inspection.features.nextn_predict_layers }}</span>.</p>
          <p class="mt-2 font-mono text-[length:var(--font-size-kicker)] text-[var(--neutral-700)]" data-testid="companion-native-mtp-params">{{ nativeMTPParamSummary() }}</p>
        </template>

        <template v-else-if="companionState(definition) === 'detected'">
          <div class="mt-4 flex items-center gap-2">
            <UInput
              :model-value="companionValue(definition)"
              class="min-w-0 flex-1 font-mono"
              :readonly="remote"
              :aria-label="`${definition.title} path`"
              @update:model-value="setCompanionValue(definition, $event)"
            />
            <AppButton type="button" intent="ghost" size="xs" @click="disableCompanion(definition)">Disable</AppButton>
          </div>
          <p class="mt-2 text-[length:var(--font-size-table-header)] text-[var(--neutral-700)]">{{ definition.title }}: {{ companionDisplayName(definition) }}<span v-if="companionSize(definition)"> · {{ formatBytes(companionSize(definition)) }}</span></p>
        </template>

        <template v-else-if="companionState(definition) === 'disabled'">
          <div v-if="!nativeMTPCompanion(definition)" class="mt-4 flex items-center gap-2">
            <UInput model-value="" class="min-w-0 flex-1 font-mono" placeholder="value cleared" readonly />
            <AppButton type="button" intent="ghost" size="xs" @click="enableCompanion(definition)">Enable</AppButton>
          </div>
          <p class="mt-2 text-[length:var(--font-size-table-header)] text-[var(--neutral-800)]" :data-testid="`companion-disabled-${definition.kind}`">{{ nativeMTPCompanion(definition) ? 'MTP defaults cleared — spec-type, spec-draft-n-max and spec-draft-p-min are not passed' : 'value cleared — the flag is not passed' }}</p>
        </template>

        <p v-else class="mt-4 text-xs text-[var(--neutral-800)]" :data-testid="`companion-empty-${definition.kind}`">No compatible {{ definition.title.toLowerCase() }} was detected in this artifact scope.</p>

        <div v-if="candidateList(definition).length > 1" class="mt-4 border-t border-[var(--color-divider)] pt-3">
          <p class="mb-2 text-[length:var(--font-size-kicker)] font-semibold uppercase tracking-[0.12em] text-[var(--neutral-700)]">Alternate candidates</p>
          <div class="flex flex-wrap gap-2">
            <UButton
              v-for="candidate in candidateList(definition)"
              :key="candidate.option_path || candidate.name"
              type="button"
              size="xs"
              :color="activeCandidate(definition)?.option_path === candidate.option_path ? 'primary' : 'neutral'"
              :variant="activeCandidate(definition)?.option_path === candidate.option_path ? 'soft' : 'ghost'"
              class="font-mono"
              :aria-pressed="activeCandidate(definition)?.option_path === candidate.option_path"
              :data-testid="`companion-candidate-${definition.kind}`"
              @click="chooseCompanionCandidate(definition, candidate)"
            >{{ candidate.quantization || candidate.name }}</UButton>
          </div>
        </div>
      </div>
    </div>
  </Frame>
</template>
