import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

const source = readFileSync(resolve(process.cwd(), 'app/components/ModelsDiscover.vue'), 'utf8')

describe('Discover redesign hierarchy', () => {
  it('uses a flat utility search row and dense repository rows', () => {
    expect(source).toContain('data-testid="discover-search-row"')
    expect(source).toContain('data-testid="discover-results"')
    expect(source).toContain('data-testid="discover-repository-row"')
    expect(source).toContain('View artifacts')
    expect(source).toContain('MODEL REGISTRY')
    expect(source).toContain('Registered models')
    expect(source).toContain('Downloads')
    expect(source).toContain('data-testid="discover-list-actions"')
    expect(source).toContain('data-testid="discover-detail-actions"')
    expect(source).toContain('w-full flex-wrap justify-start gap-2 sm:w-auto sm:shrink-0 sm:justify-end')
    expect(source).not.toContain('<UCard')
    expect(source).not.toMatch(/rounded-(?:sm|md|lg|xl|2xl|3xl|full)/)
    expect(source).not.toMatch(/gradient|backdrop-blur/)
  })

  it('keeps context and artifacts as peer top-level surfaces with semantic status', () => {
    expect(source).toContain('data-testid="discover-context-section"')
    expect(source).toContain('data-testid="discover-artifacts"')
    expect(source).toContain('<StatusTag')
    expect(source).toContain('data-testid="recommended-badge"')
    expect(source).toContain('data-testid="artifact-hardware-fit"')
    expect(source).toContain('data-testid="artifact-dependencies"')
    expect(source).toContain('Advanced details')
    expect(source).toContain('Estimated Generation')
    expect(source).toContain('Launch')
    expect(source).toContain('Download')
  })

  it('preserves URL, infinite-scroll, download and recommendation behavior hooks', () => {
    expect(source).toContain("useState<string>('models-discover-query'")
    expect(source).toContain("useState<number>('models-discover-scroll-position'")
    expect(source).toContain('data-testid="discover-load-more-sentinel"')
    expect(source).toContain('new IntersectionObserver')
    expect(source).toContain("'/api/v1/downloads'")
    expect(source).toContain("'/models/new'")
    expect(source).toContain('/api/v1/huggingface/recommendations?')
    expect(source).toContain('clearRecommendationDebounce()')
    expect(source).toContain('data-testid="discover-detail-error"')
    expect(source).toContain('data-testid="discover-detail-retry"')
    expect(source).toContain('Repository unavailable')
  })
})
