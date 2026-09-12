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

export function modelOrDetectedSource(source?: string) {
  return source === 'model' || source === 'detected'
}

export function nativeMTPParams(
  values: Record<string, string> = {},
  fallback: Record<string, string> = {}
): Record<(typeof nativeMTPOptionKeys)[number], string> {
  return {
    'spec-type': values['spec-type'] || fallback['spec-type'] || nativeMTPDefaults['spec-type'],
    'spec-draft-n-max': values['spec-draft-n-max'] || fallback['spec-draft-n-max'] || nativeMTPDefaults['spec-draft-n-max'],
    'spec-draft-p-min': values['spec-draft-p-min'] || fallback['spec-draft-p-min'] || nativeMTPDefaults['spec-draft-p-min']
  }
}

export function nativeMTPParamSummary(
  values: Record<string, string> = {},
  fallback: Record<string, string> = {}
) {
  const params = nativeMTPParams(values, fallback)
  return nativeMTPOptionKeys.map(key => `${key}=${params[key]}`).join(' · ')
}

export function looksLikeNativeMTPFilename(name: string) {
  const base = name.split(/[\\/]/).pop() || name
  const stem = base.replace(/\.gguf$/i, '').toLowerCase()
  if (!stem) return false
  if (stem === 'mtp' || stem.startsWith('mtp-') || stem.startsWith('mtp_') || stem.startsWith('mtp.')) return false
  return stem.includes('-mtp-') || stem.includes('_mtp_') || stem.endsWith('-mtp') || stem.endsWith('_mtp')
}

export function isMixedMTPRepositoryLabel(pathOrRepo: string) {
  return /(?:^|[/\\])mtp-gguf(?:[/\\]|$)|-mtp-gguf(?:[/\\]|$)/i.test(pathOrRepo)
}

export function suggestedNativeMTPFilename(name: string) {
  const base = name.split(/[\\/]/).pop() || name
  if (!base || looksLikeNativeMTPFilename(base)) return ''
  const match = base.match(/^(.+)-(Q\d+_[A-Z0-9_]+|IQ\d+_[A-Z0-9_]+|Q\d+_[A-Z]+)\.gguf$/i)
  if (!match) return ''
  const [, prefix, quant] = match
  if (!prefix || !quant || /-mtp$/i.test(prefix) || /-mtp-/i.test(prefix)) return ''
  return `${prefix}-MTP-${quant}.gguf`
}

export function hasEmbeddedMTP(
  options: Record<string, string>,
  inspection: ModelInspection | null | undefined,
  fallbackSuggested: Record<string, string> = {},
  label = ''
) {
  if (options['spec-draft-model']) return false
  if (fallbackSuggested['spec-draft-model']) return false
  if (inspection?.dependencies?.some(item => item.kind === 'mtp')) return false
  if (inspection?.features?.has_mtp && !inspection?.features?.mtp_only) return true
  if (fallbackSuggested['spec-type'] === 'draft-mtp' || options['spec-type'] === 'draft-mtp') return true
  return looksLikeNativeMTPFilename(label || inspection?.name || '')
}

export function isNativeMTPFromEffective(
  values: Record<string, string> = {},
  sources: Record<string, string> = {},
  instanceOptions: Record<string, string> = {}
) {
  const draftPath = values['spec-draft-model']
  if (draftPath && modelOrDetectedSource(sources['spec-draft-model'])) return false
  if (instanceOptions['spec-draft-model']) return false
  return values['spec-type'] === 'draft-mtp' && modelOrDetectedSource(sources['spec-type'])
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
  if (options['spec-type'] === 'draft-mtp') return true
  const label = inspection?.name || ''
  return Boolean(label && looksLikeNativeMTPFilename(label))
}
