import { describe, expect, it } from 'vitest'

import { resolveFrontendApiBase } from '../config/public-api-base'

describe('resolveFrontendApiBase', () => {
  it('prefers NUXT_PUBLIC_API_BASE when both values are set', () => {
    expect(resolveFrontendApiBase({
      NUXT_PUBLIC_API_BASE: 'http://localhost:8000',
      LLAMARACK_EXTERNAL_URL: 'https://llamarack.example.com'
    })).toBe('http://localhost:8000')
  })

  it('falls back to LLAMARACK_EXTERNAL_URL when the Nuxt override is unset', () => {
    expect(resolveFrontendApiBase({
      LLAMARACK_EXTERNAL_URL: 'https://llamarack.example.com'
    })).toBe('https://llamarack.example.com')
  })

  it('falls back to LLAMARACK_EXTERNAL_URL when the Nuxt override is empty', () => {
    expect(resolveFrontendApiBase({
      NUXT_PUBLIC_API_BASE: '',
      LLAMARACK_EXTERNAL_URL: 'https://llamarack.example.com'
    })).toBe('https://llamarack.example.com')
  })

  it('keeps the empty same-origin default when neither value is set', () => {
    expect(resolveFrontendApiBase({})).toBe('')
  })
})
