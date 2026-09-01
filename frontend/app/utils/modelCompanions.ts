export type InspectionFeatures = {
  architecture?: string
  nextn_predict_layers?: number
  has_mtp?: boolean
  mtp_only?: boolean
  projector?: boolean
}

export type CompanionFile = { path: string; size: number; oid?: string }

export type CompanionDependency = {
  kind: string
  name: string
  quantization?: string
  total_bytes: number
  files: CompanionFile[]
  option_path?: string
}

export type ModelInspection = {
  id?: string
  name?: string
  quantization?: string
  model_bytes?: number
  total_bytes?: number
  shard_count?: number
  expected_shards?: number
  complete?: boolean
  files?: CompanionFile[]
  dependencies?: CompanionDependency[]
  model_name?: string
  architecture?: string
  context_length?: number
  gguf_version?: number
  metadata_count?: number
  warning?: string
  features?: InspectionFeatures
  suggested_options?: Record<string, string>
  dependency_candidates?: CompanionDependency[]
}

export type CompanionDefinition = { key: 'mmproj' | 'spec-draft-model'; kind: 'mmproj' | 'mtp'; title: string; flag: string }

export const companionDefinitions: CompanionDefinition[] = [
  { key: 'mmproj', kind: 'mmproj', title: 'Vision projector', flag: '--mmproj' },
  { key: 'spec-draft-model', kind: 'mtp', title: 'MTP draft model', flag: '--spec-draft-model' }
]

export const companionOptionKeys = ['mmproj', 'spec-draft-model', 'spec-type']

export const nativeMTPOptionKeys = ['spec-type', 'spec-draft-n-max', 'spec-draft-p-min'] as const

export const nativeMTPDefaults: Record<(typeof nativeMTPOptionKeys)[number], string> = {
  'spec-type': 'draft-mtp',
  'spec-draft-n-max': '16',
  'spec-draft-p-min': '0.8'
}

export function isNativeMTP(
  options: Record<string, string>,
  inspection: ModelInspection | null | undefined,
  fallbackSuggested: Record<string, string> = {}
) {
  const suggested = inspection?.suggested_options || fallbackSuggested
  if (suggested['spec-draft-model'] || options['spec-draft-model']) return false
  const mtpDependency = (inspection?.dependencies || []).find(item => item.kind === 'mtp')
  if (mtpDependency) return false
  const features = inspection?.features
  if (typeof features?.has_mtp === 'boolean') return Boolean(features.has_mtp && !features.mtp_only)
  if (suggested['spec-type'] === 'draft-mtp') return true
  return options['spec-type'] === 'draft-mtp'
}
