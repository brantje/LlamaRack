import type { Profile } from '~/composables/useManager'

const managerOwnedOptions = new Set(['model', 'host', 'port', 'device', 'tensor-split'])

type ProfileOption = Profile['options'][number] & { kind?: string; choices?: string[] }

function inferredKind(option: ProfileOption) {
  if (option.kind) return option.kind
  const hint = (option.value_hint || '').trim()
  if (!hint) return 'boolean'
  const unwrapped = hint.replace(/^[\[<]|[\]>]$/g, '')
  if (unwrapped.includes('|') && !/\s/.test(unwrapped)) return 'enum'
  const lower = unwrapped.toLowerCase()
  if (lower === 'n' || lower === 'int' || lower === 'integer' || lower.endsWith('_int')) return 'integer'
  if (lower === 'f' || lower === 'float' || lower === 'number' || lower.endsWith('_float')) return 'number'
  if (lower === 'bool' || lower === 'boolean') return 'boolean'
  return 'string'
}

function choicesFor(option: ProfileOption) {
  if (option.choices?.length) return option.choices
  const hint = (option.value_hint || '').trim().replace(/^[\[<]|[\]>]$/g, '')
  return hint.includes('|') ? hint.split('|').map(value => value.trim()).filter(Boolean) : []
}

function validateValue(option: ProfileOption, value: string) {
  switch (inferredKind(option)) {
    case 'boolean':
      if (value !== 'true' && value !== 'false') throw new Error(`llama.cpp option “${option.key}” must be true or false`)
      break
    case 'integer':
      if (!/^[+-]?\d+$/.test(value)) throw new Error(`llama.cpp option “${option.key}” requires an integer value`)
      break
    case 'number':
      if (!value || !Number.isFinite(Number(value))) throw new Error(`llama.cpp option “${option.key}” requires a numeric value`)
      break
    case 'enum': {
      const choices = choicesFor(option)
      if (!choices.includes(value)) throw new Error(`llama.cpp option “${option.key}” must be one of: ${choices.join(', ')}`)
      break
    }
    default:
      if (!value) throw new Error(`llama.cpp option “${option.key}” requires ${option.value_hint || 'a value'}`)
  }
}

export function parseLlamaCppOptions(text: string, profile: Profile | null) {
  const parsed: Record<string, string> = {}
  const options = (profile?.options || []) as ProfileOption[]
  const available = new Map(options.map(option => [option.key, option]))

  for (const line of text.split('\n')) {
    const trimmed = line.trim()
    if (!trimmed) continue
    const at = trimmed.indexOf('=')
    if (at <= 0) throw new Error(`Invalid option “${trimmed}”; use key=value`)
    const key = trimmed.slice(0, at).trim().replace(/^-+/, '')
    const value = trimmed.slice(at + 1).trim()
    if (!key) throw new Error(`Invalid option “${trimmed}”; option key is required`)
    if (key in parsed) throw new Error(`Duplicate llama.cpp option “${key}”`)
    if (managerOwnedOptions.has(key)) throw new Error(`llama.cpp option “${key}” is managed by LlamaCPP Manager and cannot be overridden here`)

    if (profile) {
      const option = available.get(key)
      if (!option) throw new Error(`Unsupported llama.cpp option “${key}” for ${profile.version || profile.path}`)
      validateValue(option, value)
    }
    parsed[key] = value
  }

  return parsed
}
