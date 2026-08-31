import { describe, expect, it } from 'vitest'
import { isNativeMTP } from '~/utils/modelCompanions'

describe('model companion helpers', () => {
  it('treats inspect features as built-in MTP unless a draft sidecar exists', () => {
    expect(isNativeMTP({}, { features: { has_mtp: true, mtp_only: false } })).toBe(true)
    expect(isNativeMTP({}, { features: { has_mtp: true, mtp_only: true } })).toBe(false)
    expect(isNativeMTP({}, { features: { has_mtp: false } })).toBe(false)
    expect(isNativeMTP({}, { features: { has_mtp: true, mtp_only: false }, suggested_options: { 'spec-draft-model': '/models/draft.gguf' } })).toBe(false)
    expect(isNativeMTP({ 'spec-draft-model': '/models/draft.gguf' }, { features: { has_mtp: true, mtp_only: false } })).toBe(false)
    expect(isNativeMTP({}, { dependencies: [{ kind: 'mtp', name: 'draft.gguf', total_bytes: 1, files: [] }], features: { has_mtp: true, mtp_only: false } })).toBe(false)
  })

  it('falls back to suggested or saved spec-type when inspect omits features', () => {
    expect(isNativeMTP({}, { suggested_options: { 'spec-type': 'draft-mtp' } })).toBe(true)
    expect(isNativeMTP({ 'spec-type': 'draft-mtp' }, null)).toBe(true)
    expect(isNativeMTP({}, null, { 'spec-type': 'draft-mtp' })).toBe(true)
    expect(isNativeMTP({}, { suggested_options: { 'spec-type': 'draft-mtp', 'spec-draft-model': '/models/draft.gguf' } })).toBe(false)
    expect(isNativeMTP({}, null)).toBe(false)
  })
})
