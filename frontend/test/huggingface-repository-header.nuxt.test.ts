import { describe, expect, it } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { mountSuspended } from '@nuxt/test-utils/runtime'
import ModelsDiscoverRepositoryHeader from '~/components/ModelsDiscoverRepositoryHeader.vue'

const longDescription = [
  'Important limitations: quantization does not repair the behavioral limitations of the source model.',
  'This repository contains high-quality GGUF quantizations of the complete merged BF16 Carnice-V3 checkpoint.',
  'Carnice V3 is based on Qwen/Qwen3.8-27B and is intended for agent workloads with explicit runtime controls.',
  'The published variants provide different memory and quality trade-offs while sharing the same source checkpoint.'
].join('\n\n')

function model(overrides: Record<string, any> = {}) {
  return {
    id: 'kai-os/Carnice-V3-GGUF',
    author: 'kai-os',
    description: longDescription,
    downloads: 577,
    likes: 20,
    last_modified: new Date(Date.now() - 2 * 60 * 60 * 1000).toISOString(),
    parameter_count: 27_315_000_000,
    tags: ['gguf', 'qwen3.8', 'hermes-agent', 'tool-use', 'llama.cpp', 'quantized', 'license:apache-2.0'],
    private: false,
    gated: false,
    card_metadata: {
      license: 'apache-2.0',
      pipeline_tag: 'image-text-to-text',
      library_name: 'gguf',
      base_models: ['kai-os/Carnice-V3'],
      languages: ['en']
    },
    ...overrides
  }
}

const recommendations = {
  context_capability: 262144,
  metadata: { architecture: 'qwen35', context_length: 262144 }
}

describe('Discover repository metadata header', () => {
  it('combines provider card metadata with GGUF recommendation metadata', async () => {
    const wrapper = await mountSuspended(ModelsDiscoverRepositoryHeader, {
      props: { model: model(), recommendations }
    })

    const metadata = wrapper.get('[data-testid="repository-metadata"]')
    expect(metadata.text()).toContain('27.3B params')
    expect(metadata.text()).toContain('qwen35')
    expect(metadata.text()).toContain('256K')
    expect(metadata.text()).toContain('image-text-to-text')
    expect(metadata.text()).toContain('apache-2.0')
    expect(metadata.text()).toContain('gguf')
    expect(metadata.text()).toContain('577')
    expect(metadata.text()).toContain('20')

    const tags = wrapper.get('[data-testid="repository-tags"]')
    expect(tags.text()).toContain('kai-os/Carnice-V3')
    expect(tags.text()).toContain('EN')
    expect(tags.text()).toContain('qwen3.8')
    expect(tags.text()).toContain('tool-use')
    expect(tags.text()).not.toContain('license:apache-2.0')

    expect(wrapper.get('[data-testid="repository-description"]').text()).toContain('Important limitations')
    expect(wrapper.text()).toContain('Read full description')
    const button = wrapper.findAll('button').find(item => item.text().trim() === 'Read full description')
    expect(button).toBeTruthy()
    await button!.trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="repository-description-full"]').text()).toContain('published variants provide different memory')
    wrapper.unmount()
  })

  it('keeps short descriptions simple and tolerates sparse metadata', async () => {
    const wrapper = await mountSuspended(ModelsDiscoverRepositoryHeader, {
      props: {
        model: model({
          description: 'Short provider description.',
          author: '',
          last_modified: 'invalid',
          parameter_count: 0,
          tags: [],
          downloads: 0,
          likes: 0,
          card_metadata: undefined,
          private: true,
          gated: true
        }),
        recommendations: null
      }
    })

    expect(wrapper.text()).toContain('Short provider description.')
    expect(wrapper.text()).not.toContain('Read full description')
    expect(wrapper.text()).toContain('Private')
    expect(wrapper.text()).toContain('Gated')
    expect(wrapper.text()).not.toContain('Architecture')
    wrapper.unmount()
  })
})
