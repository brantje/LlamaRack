import { afterEach, describe, expect, it, vi } from 'vitest'
import { newPlaygroundSessionID } from '~/utils/playgroundSession'

describe('newPlaygroundSessionID', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('uses crypto.randomUUID when available', () => {
    vi.stubGlobal('crypto', { randomUUID: () => '11111111-2222-3333-4444-555555555555' })
    expect(newPlaygroundSessionID()).toBe('11111111-2222-3333-4444-555555555555')
  })

  it('falls back to a pg- prefixed id when randomUUID is missing', () => {
    vi.stubGlobal('crypto', {})
    expect(newPlaygroundSessionID()).toMatch(/^pg-[0-9a-f]+-[0-9a-f]+$/)
  })
})
