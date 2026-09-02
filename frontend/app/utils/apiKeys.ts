import { CalendarDate, type DateValue } from '@internationalized/date'
import type { APIKey, APIKeyOwnerKind, APIKeyType, ServiceAccount, User } from '~/composables/useManager'

export const API_KEY_TYPE_ITEMS: Array<{ label: string; value: APIKeyType; description: string }> = [
  { label: 'Inference', value: 'inference', description: 'OpenAI-compatible /v1/* only' },
  { label: 'Management', value: 'management', description: '/api/v1/* except session, Playground, and service-account admin; cannot call /v1/*' },
  { label: 'Full Access', value: 'full', description: 'Both /v1/* and /api/v1/* except session and Playground. Can manage service accounts.' }
]

export const API_KEY_TYPE_TOOLTIP = 'Inference keys call OpenAI-compatible /v1/* only. Management keys call /api/v1/* except session, Playground, and service-account admin, and cannot call /v1/*. Full Access can call both planes except session and Playground, and can manage service accounts.'

export type APIKeyDraft = {
  name: string
  key_type: APIKeyType
  owner_user_id?: number | null
  owner_service_account_id?: string | null
  instance_ids?: string[]
  expires_on?: string | null
}

type OwnerCandidate = { id: string | number; name: string; enabled: boolean; kind: APIKeyOwnerKind }

export function padDatePart(value: number) {
  return String(value).padStart(2, '0')
}

export function utcDateString(now = new Date()) {
  return `${now.getUTCFullYear()}-${padDatePart(now.getUTCMonth() + 1)}-${padDatePart(now.getUTCDate())}`
}

export function apiKeyTypeLabel(type?: string) {
  return API_KEY_TYPE_ITEMS.find(item => item.value === type)?.label || type || '—'
}

export function isAPIKeyExpired(expiresOn?: string | null, now = new Date()) {
  if (!expiresOn) return false
  return expiresOn < utcDateString(now)
}

export function isAPIKeyDateUnavailable(date: DateValue, now = new Date()) {
  const value = `${date.year}-${padDatePart(date.month)}-${padDatePart(date.day)}`
  return value < utcDateString(now)
}

export function apiKeyStatus(key: Pick<APIKey, 'enabled' | 'owner_enabled' | 'expires_on'> & { status?: string }) {
  if (key.status === 'disabled') return { label: 'Disabled', variant: 'neutral' as const }
  if (key.status === 'owner_disabled') return { label: 'Owner disabled', variant: 'pending' as const }
  if (key.status === 'expired') return { label: 'Expired', variant: 'failed' as const }
  if (key.status === 'enabled') return { label: 'Enabled', variant: 'ready' as const }
  if (!key.enabled) return { label: 'Disabled', variant: 'neutral' as const }
  if (key.owner_enabled === false) return { label: 'Owner disabled', variant: 'pending' as const }
  if (isAPIKeyExpired(key.expires_on)) return { label: 'Expired', variant: 'failed' as const }
  return { label: 'Enabled', variant: 'ready' as const }
}

export function formatAPIKeyPrefix(prefix: string) {
  return prefix.endsWith('…') ? prefix : `${prefix}…`
}

export function formatAPIKeyTimestamp(value?: number | null) {
  if (!value) return '—'
  const date = new Date(value * 1000)
  return Number.isNaN(date.getTime()) ? '—' : date.toLocaleString()
}

export function encodeAPIKeyOwner(kind: APIKeyOwnerKind, id: string | number) {
  return `${kind}:${id}`
}

export function decodeAPIKeyOwner(value: string) {
  if (value.startsWith('user:')) {
    const id = Number(value.slice(5))
    return Number.isFinite(id) ? { owner_user_id: id as number | null, owner_service_account_id: null as string | null } : {}
  }
  if (value.startsWith('service_account:')) {
    const id = value.slice('service_account:'.length)
    return id ? { owner_user_id: null as number | null, owner_service_account_id: id } : {}
  }
  return {}
}

export function ownerValueForKey(key: Pick<APIKey, 'owner_kind' | 'owner_id'>) {
  return encodeAPIKeyOwner(key.owner_kind, key.owner_id)
}

export function calendarDateToExpiresOn(value?: { year: number; month: number; day: number } | null) {
  if (!value) return null
  return `${value.year}-${padDatePart(value.month)}-${padDatePart(value.day)}`
}

export function expiresOnToCalendarDate(value?: string | null) {
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(value || '')
  if (!match) return undefined
  return new CalendarDate(Number(match[1]), Number(match[2]), Number(match[3]))
}

export function enabledOwnerItems(
  users: Array<Pick<User, 'id' | 'username' | 'enabled'>>,
  serviceAccounts: Array<Pick<ServiceAccount, 'id' | 'name' | 'enabled'>>,
  current?: { kind: APIKeyOwnerKind; id: string | number }
) {
  const keep = (item: OwnerCandidate) => item.enabled || (current != null && item.kind === current.kind && String(item.id) === String(current.id))
  const userItems = users
    .map(user => ({ id: user.id, name: user.username, enabled: user.enabled, kind: 'user' as const }))
    .filter(keep)
    .map(user => ({ label: user.name, value: encodeAPIKeyOwner('user', user.id) }))
  const accountItems = serviceAccounts
    .map(account => ({ id: account.id, name: account.name, enabled: account.enabled, kind: 'service_account' as const }))
    .filter(keep)
    .map(account => ({ label: account.name, value: encodeAPIKeyOwner('service_account', account.id) }))
  const groups: Array<Array<{ type?: 'label'; label: string; value?: string }>> = []
  if (userItems.length) groups.push([{ type: 'label', label: 'Users' }, ...userItems])
  if (accountItems.length) groups.push([{ type: 'label', label: 'Service accounts' }, ...accountItems])
  return groups
}
