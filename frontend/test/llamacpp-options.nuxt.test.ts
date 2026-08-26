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
    { key: 'cache-type-k', value_hint: '<f16|q8_0>', kind: 'enum', choices: ['f16', 'q8_0'] },
    { key: 'chat-template', value_hint: 'STRING', kind: 'string' }
  ]
} as Profile

describe('llama.cpp option form validation', () => {
  it('canonicalizes supported options and validates discovered types', () => {
    expect(parseLlamaCppOptions('--ctx-size=8192\ntemperature=0.7\nflash-attn=true\ncache-type-k=q8_0\nchat-template=chatml', profile)).toEqual({
      'ctx-size': '8192',
      temperature: '0.7',
      'flash-attn': 'true',
      'cache-type-k': 'q8_0',
      'chat-template': 'chatml'
    })
  })

  it('rejects unknown, duplicate, reserved and invalid values', () => {
    const cases = [
      ['made-up=1', 'Unsupported llama.cpp option'],
      ['ctx-size=1\n--ctx-size=2', 'Duplicate llama.cpp option'],
      ['port=8000', 'managed by LlamaCPP Manager'],
      ['ctx-size=many', 'integer value'],
      ['temperature=warm', 'numeric value'],
      ['flash-attn=yes', 'true or false'],
      ['cache-type-k=q4_0', 'must be one of'],
      ['chat-template=', 'requires STRING'],
      ['broken-line', 'use key=value']
    ] as const
    for (const [value, message] of cases) {
      expect(() => parseLlamaCppOptions(value, profile)).toThrow(message)
    }
  })

  it('still performs structural and manager-owned validation when discovery is unavailable', () => {
    expect(parseLlamaCppOptions('ctx-size=8192', null)).toEqual({ 'ctx-size': '8192' })
    expect(() => parseLlamaCppOptions('device=0', null)).toThrow('managed by LlamaCPP Manager')
  })

  it('supports profiles without the new kind metadata', () => {
    const legacy = {
      path: '/old/server', fingerprint: 'legacy', options: [
        { key: 'threads', value_hint: 'N' },
        { key: 'mode', value_hint: '<fast|safe>' },
        { key: 'verbose' }
      ]
    } satisfies Profile
    expect(parseLlamaCppOptions('threads=4\nmode=fast\nverbose=false', legacy)).toEqual({ threads: '4', mode: 'fast', verbose: 'false' })
  })
})
