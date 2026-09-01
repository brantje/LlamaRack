import { describe, expect, it } from 'vitest'
import { parseLlamaCppOptions } from '~/utils/llamacppOptions'
import type { Profile } from '~/composables/useManager'

const profile = {
  path: '/app/llama-server',
  version: 'llama.cpp test',
  fingerprint: 'abc',
  options: [
    { key: 'ctx-size', value_hint: 'N', kind: 'integer' },
    { key: 'temperature', value_hint: 'FLOAT', kind: 'number' },
    { key: 'flash-attn', kind: 'boolean' },
    { key: 'no-flash-attn', kind: 'boolean' },
    { key: 'cache-type-k', value_hint: '<f16|q8_0>', kind: 'enum', choices: ['f16', 'q8_0'] },
    { key: 'chat-template', value_hint: 'STRING', kind: 'string' },
    { key: 'tensor-split', value_hint: 'SPLIT', kind: 'string' },
    { key: 'prompt', kind: 'string' },
    { key: 'server-owned', value_hint: 'STRING', kind: 'string', manager_owned: true }
  ]
} as Profile

describe('llama.cpp option form validation', () => {
  it('canonicalizes supported options and validates discovered types', () => {
    expect(parseLlamaCppOptions('\n--ctx-size=8192\ntemperature=0.7\nflash-attn=true\ncache-type-k=q8_0\nchat-template=chatml\ntensor-split=3,1\n', profile)).toEqual({
      'ctx-size': '8192',
      temperature: '0.7',
      'flash-attn': 'true',
      'cache-type-k': 'q8_0',
      'chat-template': 'chatml',
      'tensor-split': '3,1'
    })
    expect(parseLlamaCppOptions('flash-attn=false', profile)).toEqual({ 'flash-attn': 'false' })
    expect(parseLlamaCppOptions('', null)).toEqual({})
  })

  it('rejects unknown, duplicate, reserved and invalid values', () => {
    const cases = [
      ['made-up=1', 'Unsupported llama.cpp option'],
      ['ctx-size=1\n--ctx-size=2', 'Duplicate llama.cpp option'],
      ['port=8000', 'managed by LlamaRack'],
      ['server-owned=value', 'managed by LlamaRack'],
      ['ctx-size=many', 'integer value'],
      ['temperature=warm', 'numeric value'],
      ['temperature=', 'numeric value'],
      ['flash-attn=yes', 'true or false'],
      ['cache-type-k=q4_0', 'must be one of'],
      ['chat-template=', 'requires STRING'],
      ['prompt=', 'requires a value'],
      ['broken-line', 'use key=value'],
      ['---=value', 'option key is required']
    ] as const
    for (const [value, message] of cases) {
      expect(() => parseLlamaCppOptions(value, profile)).toThrow(message)
    }
  })

  it('rejects explicit false when no inverse flag exists', () => {
    const noInverse = {
      path: '/app/llama-server', version: 'test', fingerprint: 'x',
      options: [{ key: 'embeddings', kind: 'boolean' }]
    } as Profile
    expect(() => parseLlamaCppOptions('embeddings=false', noInverse)).toThrow('inverse flag --no-embeddings is unavailable')
  })

  it('requires the detected schema for non-empty overrides', () => {
    expect(() => parseLlamaCppOptions('ctx-size=8192', null)).toThrow('option schema is unavailable')
    expect(() => parseLlamaCppOptions('device=0', null)).toThrow('managed by LlamaRack')
  })

  it('uses the profile path when an unknown option is rejected without a version', () => {
    const pathOnly = { ...profile, version: undefined }
    expect(() => parseLlamaCppOptions('made-up=1', pathOnly)).toThrow('/app/llama-server')
  })

  it('supports profiles without the new kind metadata and all inferred scalar aliases', () => {
    const legacy = {
      path: '/old/server', fingerprint: 'legacy', options: [
        { key: 'threads', value_hint: 'N' },
        { key: 'threads-int', value_hint: 'INT' },
        { key: 'workers', value_hint: 'INTEGER' },
        { key: 'batch-int', value_hint: 'BATCH_INT' },
        { key: 'temperature-f', value_hint: 'F' },
        { key: 'temperature-float', value_hint: 'FLOAT' },
        { key: 'scale', value_hint: 'NUMBER' },
        { key: 'ratio-float', value_hint: 'RATIO_FLOAT' },
        { key: 'enabled-bool', value_hint: 'BOOL' },
        { key: 'enabled-boolean', value_hint: 'BOOLEAN' },
        { key: 'no-enabled-boolean' },
        { key: 'mode', value_hint: '<fast|safe>' },
        { key: 'mode-empty-choice', value_hint: '<fast||safe>' },
        { key: 'pipe-label', value_hint: 'fast | safe' },
        { key: 'verbose' },
        { key: 'no-verbose' },
        { key: 'label', value_hint: 'STRING' }
      ]
    } satisfies Profile

    expect(parseLlamaCppOptions([
      'threads=4', 'threads-int=4', 'workers=4', 'batch-int=4',
      'temperature-f=0.1', 'temperature-float=0.2', 'scale=1.5', 'ratio-float=0.3',
      'enabled-bool=true', 'enabled-boolean=false', 'mode=fast', 'mode-empty-choice=safe',
      'pipe-label=fast | safe', 'verbose=false', 'label=test'
    ].join('\n'), legacy)).toEqual({
      threads: '4', 'threads-int': '4', workers: '4', 'batch-int': '4',
      'temperature-f': '0.1', 'temperature-float': '0.2', scale: '1.5', 'ratio-float': '0.3',
      'enabled-bool': 'true', 'enabled-boolean': 'false', mode: 'fast', 'mode-empty-choice': 'safe',
      'pipe-label': 'fast | safe', verbose: 'false', label: 'test'
    })
  })

  it('covers inferred enum choice fallback failures', () => {
    const legacyEnum = {
      path: '/old/server', fingerprint: 'legacy', options: [
        { key: 'mode', value_hint: '<fast|safe>' },
        { key: 'forced-enum', value_hint: 'VALUE', kind: 'enum' }
      ]
    } as Profile
    expect(() => parseLlamaCppOptions('mode=slow', legacyEnum)).toThrow('must be one of: fast, safe')
    expect(() => parseLlamaCppOptions('forced-enum=x', legacyEnum)).toThrow('must be one of: ')
  })
})
