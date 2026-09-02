import { describe, expect, it } from 'vitest'
import { CalendarDate } from '@internationalized/date'
import {
  API_KEY_DATE_LOCALE,
  API_KEY_TYPE_ITEMS,
  API_KEY_TYPE_TOOLTIP,
  apiKeyStatus,
  apiKeyTypeLabel,
  calendarDateToExpiresOn,
  decodeAPIKeyOwner,
  enabledOwnerItems,
  encodeAPIKeyOwner,
  expiresOnToCalendarDate,
  formatAPIKeyPrefix,
  formatAPIKeyTimestamp,
  formatExpiresOnDisplay,
  isAPIKeyDateUnavailable,
  isAPIKeyExpired,
  ownerValueForKey,
  utcDateString
} from '~/utils/apiKeys'

describe('API key presentation helpers', () => {
  it('formats type labels, prefixes and timestamps', () => {
    expect(apiKeyTypeLabel('inference')).toBe('Inference')
    expect(apiKeyTypeLabel('management')).toBe('Management')
    expect(apiKeyTypeLabel('full')).toBe('Full Access')
    expect(apiKeyTypeLabel('unknown')).toBe('unknown')
    expect(apiKeyTypeLabel()).toBe('—')
    expect(formatAPIKeyPrefix('sk-abcd1234')).toBe('sk-abcd1234…')
    expect(formatAPIKeyPrefix('sk-abcd1234…')).toBe('sk-abcd1234…')
    expect(formatAPIKeyTimestamp()).toBe('—')
    expect(formatAPIKeyTimestamp(0)).toBe('—')
    expect(formatAPIKeyTimestamp(1_700_000_000)).toBe(new Date(1_700_000_000 * 1000).toLocaleString())
    expect(formatAPIKeyTimestamp(Number.POSITIVE_INFINITY)).toBe('—')
    expect(formatExpiresOnDisplay('2026-01-09')).toBe('09-01-2026')
    expect(formatExpiresOnDisplay('2027-01-01')).toBe('01-01-2027')
    expect(formatExpiresOnDisplay(null)).toBe('—')
    expect(formatExpiresOnDisplay()).toBe('—')
    expect(formatExpiresOnDisplay('nope')).toBe('nope')
    expect(API_KEY_DATE_LOCALE).toBe('en-GB')
    expect(API_KEY_TYPE_ITEMS.map(item => item.label)).toEqual(['Inference', 'Management', 'Full Access'])
    expect(API_KEY_TYPE_ITEMS.find(item => item.value === 'inference')?.description).toBe('OpenAI-compatible /v1/* only')
    expect(API_KEY_TYPE_ITEMS.find(item => item.value === 'management')?.description).toContain('cannot call /v1/*')
    expect(API_KEY_TYPE_ITEMS.find(item => item.value === 'management')?.description).toContain('service-account admin')
    expect(API_KEY_TYPE_ITEMS.find(item => item.value === 'full')?.description).toContain('Both /v1/* and /api/v1/*')
    expect(API_KEY_TYPE_ITEMS.find(item => item.value === 'full')?.description).toContain('Can manage service accounts')
    expect(API_KEY_TYPE_ITEMS.find(item => item.value === 'full')?.description).not.toMatch(/except session, Playground, and service-account admin/)
    expect(API_KEY_TYPE_ITEMS.find(item => item.value === 'full')?.description).not.toMatch(/user-owned/i)
    expect(API_KEY_TYPE_TOOLTIP).toContain('cannot call /v1/*')
    expect(API_KEY_TYPE_TOOLTIP).toContain('Full Access can call both planes except session and Playground, and can manage service accounts')
    expect(API_KEY_TYPE_TOOLTIP).not.toMatch(/signed-in browser session/)
    expect(API_KEY_TYPE_TOOLTIP).not.toMatch(/user-owned/i)
  })

  it('computes status with Disabled, Owner disabled, then Expired precedence', () => {
    expect(apiKeyStatus({ enabled: false, owner_enabled: false, expires_on: '1999-01-01' })).toEqual({ label: 'Disabled', variant: 'neutral' })
    expect(apiKeyStatus({ enabled: true, owner_enabled: false, expires_on: '1999-01-01' })).toEqual({ label: 'Owner disabled', variant: 'pending' })
    expect(apiKeyStatus({ enabled: true, owner_enabled: true, expires_on: '1999-01-01' })).toEqual({ label: 'Expired', variant: 'failed' })
    expect(apiKeyStatus({ enabled: true, owner_enabled: true, expires_on: utcDateString() })).toEqual({ label: 'Enabled', variant: 'ready' })
    expect(apiKeyStatus({ enabled: true, owner_enabled: true, status: 'owner_disabled' })).toEqual({ label: 'Owner disabled', variant: 'pending' })
    expect(apiKeyStatus({ enabled: true, owner_enabled: true, status: 'enabled' })).toEqual({ label: 'Enabled', variant: 'ready' })
  })

  it('treats expiry as valid through the end of the UTC day', () => {
    const now = new Date('2026-09-02T23:30:00Z')
    expect(isAPIKeyExpired('2026-09-02', now)).toBe(false)
    expect(isAPIKeyExpired('2026-09-01', now)).toBe(true)
    expect(isAPIKeyExpired(null, now)).toBe(false)
    expect(isAPIKeyDateUnavailable(new CalendarDate(2026, 9, 1), now)).toBe(true)
    expect(isAPIKeyDateUnavailable(new CalendarDate(2026, 9, 2), now)).toBe(false)
  })

  it('encodes owners and grouped picker items for enabled principals plus the current owner', () => {
    expect(encodeAPIKeyOwner('user', 3)).toBe('user:3')
    expect(decodeAPIKeyOwner('user:3')).toEqual({ owner_user_id: 3, owner_service_account_id: null })
    expect(decodeAPIKeyOwner('service_account:sa-9')).toEqual({ owner_user_id: null, owner_service_account_id: 'sa-9' })
    expect(decodeAPIKeyOwner('user:nope')).toEqual({})
    expect(decodeAPIKeyOwner('service_account:')).toEqual({})
    expect(decodeAPIKeyOwner('other')).toEqual({})
    expect(ownerValueForKey({ owner_kind: 'service_account', owner_id: 'bot' })).toBe('service_account:bot')
    expect(calendarDateToExpiresOn(new CalendarDate(2026, 1, 9))).toBe('2026-01-09')
    expect(calendarDateToExpiresOn(undefined)).toBeNull()
    expect(expiresOnToCalendarDate('2026-01-09')?.toString()).toBe('2026-01-09')
    expect(expiresOnToCalendarDate('nope')).toBeUndefined()

    const items = enabledOwnerItems(
      [{ id: 1, username: 'admin', enabled: true }, { id: 2, username: 'off', enabled: false }],
      [{ id: 'sa-1', name: 'CI', enabled: true }, { id: 'sa-2', name: 'Old', enabled: false }],
      { kind: 'service_account', id: 'sa-2' }
    )
    expect(items[0]?.[0]).toEqual({ type: 'label', label: 'Users' })
    expect(items[0]?.map(item => item.value).filter(Boolean)).toEqual(['user:1'])
    expect(items[1]?.[0]).toEqual({ type: 'label', label: 'Service accounts' })
    expect(items[1]?.map(item => item.value).filter(Boolean)).toEqual(['service_account:sa-1', 'service_account:sa-2'])
  })
})
