import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

const source = readFileSync(resolve(process.cwd(), 'app/pages/downloads.vue'), 'utf8')

describe('Downloads redesign cleanup', () => {
  it('uses semantic framed notes without nesting a Frame inside download jobs', () => {
    expect(source).toContain('data-testid="downloads-error"')
    expect(source).toContain('<StatusTag variant="failed">Download error</StatusTag>')
    expect(source).toContain('data-testid="download-failure-note"')
    expect(source).toContain('<StatusTag variant="failed">Download failed</StatusTag>')
    expect(source).not.toContain('<Frame v-if="job.error"')
    expect(source).not.toContain('border-[var(--accent-800)]')
  })

  it('maps only active download states to pending and falls back to neutral', () => {
    expect(source).toContain("if (activeStates.has(state)) return 'pending'")
    expect(source).toContain("return 'neutral'")
  })

  it('keeps square byte-based progress and file disclosure hooks', () => {
    expect(source).toContain('data-testid="download-progress-track"')
    expect(source).toContain('data-testid="download-progress-fill"')
    expect(source).toContain('data-testid="download-files"')
    expect(source).toContain('Math.min(100, Math.max(0, Math.round(job.downloaded_bytes / job.total_bytes * 100)))')
  })
})
