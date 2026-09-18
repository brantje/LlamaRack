import { describe, expect, it } from 'vitest'
import {
  benchmarkComparisonDifferences,
  benchmarkConfigChanges,
  benchmarkControlledConfigChange,
  benchmarkEffectiveConfig,
  benchmarkTuningHints,
  type BenchmarkRun
} from '~/composables/useBenchmarks'

function runFixture(): BenchmarkRun {
  return {
    id: 'run-a',
    instance_id: 'instance-a',
    instance_slug_snapshot: 'instance-a',
    instance_name_snapshot: 'Instance A',
    instance_config_snapshot: {
      schema_version: 1,
      gpu_mode: 'manual',
      gpu_devices: ['CUDA0'],
      tensor_split: '1',
      options: { 'batch-size': '512', threads: '4' },
      sources: { 'batch-size': 'instance', threads: 'instance' }
    },
    model_id: 'model-a',
    model_slug_snapshot: 'model-a',
    model_name_snapshot: 'Model A',
    artifact_snapshot: { path: 'model.gguf', fingerprint: 'artifact', size: 1 },
    workload_profile: {
      id: 'prompt-heavy-v1',
      version: 1,
      prompt_tokens: [1024, 2048],
      generation_tokens: [128],
      repetitions: 5,
      warmup: true
    },
    resolved_argv: ['llama-bench'],
    status: 'COMPLETED',
    created_at: '2026-09-09T10:00:00Z',
    build: { runtime_variant: 'cuda', llama_cpp_build: 'b9999', llama_bench_version: 'b9999', llama_bench_fingerprint: 'bench' },
    hardware_snapshot: {
      observed: {
        ram_total_bytes: 1024,
        ram_available_bytes: 512,
        collected_at: '2026-09-09T10:00:00Z',
        processes: [],
        gpus: [{ id: 'CUDA0', backend: 'CUDA', index: 0, name: 'GPU', total_bytes: 1024, used_bytes: 100, free_bytes: 924, utilization_pct: 0 }]
      },
      cpu: { model: 'CPU', logical_threads: 16, effective_threads: 4, architecture: 'amd64', os: 'linux' },
      selected_devices: ['CUDA0']
    },
    results: []
  }
}

describe('benchmark tuning guidance', () => {
  it('uses persisted workload-specific tuning hints and falls back for old runs', () => {
    const run = runFixture()
    run.workload_profile.tuning_hints = [{ key: 'batch-size', impact: 'Prompt processing', reason: 'Measure it.' }]
    expect(benchmarkTuningHints(run)).toEqual(run.workload_profile.tuning_hints)

    delete run.workload_profile.tuning_hints
    expect(benchmarkTuningHints(run).map(hint => hint.key)).toContain('batch-size')
  })

  it('ignores descriptive workload metadata when deciding comparability', () => {
    const left = runFixture()
    const right = structuredClone(left)
    right.workload_profile.name = 'Prompt-heavy / RAG'
    right.workload_profile.description = 'Updated copy only'
    right.workload_profile.tuning_hints = [{ key: 'batch-size', impact: 'Prompt', reason: 'Updated guidance' }]
    expect(benchmarkComparisonDifferences(left, right)).toEqual([])
  })

  it('keeps legacy benchmark history readable and identifies old controlled settings', () => {
    const left = runFixture()
    const right = structuredClone(left)
    right.id = 'run-b'
    right.instance_config_snapshot.options!['batch-size'] = '1024'

    expect(benchmarkEffectiveConfig(left)).toBe(left.instance_config_snapshot)
    expect(benchmarkConfigChanges(left, right)).toEqual([
      { key: 'batch-size', label: '--batch-size', before: '512', after: '1024', tunable: true }
    ])
    expect(benchmarkControlledConfigChange(left, right)).toEqual({
      key: 'batch-size', label: '--batch-size', before: '512', after: '1024', tunable: true
    })
  })

  it('uses immutable effective runtime snapshots for new run-scoped overrides', () => {
    const left = runFixture()
    left.effective_benchmark_config = structuredClone(left.instance_config_snapshot)
    left.effective_benchmark_config.options!['ctx-size'] = '2048'
    left.effective_benchmark_config.sources!['ctx-size'] = 'instance'
    const right = structuredClone(left)
    right.id = 'run-b'
    right.benchmark_overrides = { context_size: 4096 }
    right.effective_benchmark_config!.options!['ctx-size'] = '4096'
    right.effective_benchmark_config!.sources!['ctx-size'] = 'benchmark'

    expect(right.instance_config_snapshot.options?.['ctx-size']).toBeUndefined()
    expect(benchmarkComparisonDifferences(left, right)).toEqual(['Runtime configuration'])
    expect(benchmarkConfigChanges(left, right)).toContainEqual({
      key: 'ctx-size', label: '--ctx-size', before: '2048', after: '4096', tunable: true
    })
    expect(benchmarkControlledConfigChange(left, right)?.key).toBe('ctx-size')
  })

  it('refuses attribution when multiple settings changed', () => {
    const left = runFixture()
    const right = structuredClone(left)
    right.instance_config_snapshot.options!['batch-size'] = '1024'
    right.instance_config_snapshot.options!.threads = '8'
    right.hardware_snapshot.cpu!.effective_threads = 8
    expect(benchmarkConfigChanges(left, right)).toHaveLength(2)
    expect(benchmarkControlledConfigChange(left, right)).toBeUndefined()
  })

  it('treats effective thread count as runtime configuration, not changed CPU hardware', () => {
    const left = runFixture()
    const right = structuredClone(left)
    right.instance_config_snapshot.options!.threads = '8'
    right.hardware_snapshot.cpu!.effective_threads = 8
    expect(benchmarkComparisonDifferences(left, right)).toEqual(['Instance configuration'])
    expect(benchmarkControlledConfigChange(left, right)?.key).toBe('threads')
  })

  it('does not attribute legacy ctx-size changes without persisted benchmark override evidence', () => {
    const left = runFixture()
    const right = structuredClone(left)
    left.instance_config_snapshot.options!['ctx-size'] = '2048'
    right.instance_config_snapshot.options!['ctx-size'] = '4096'
    expect(benchmarkControlledConfigChange(left, right)).toBeUndefined()
  })
})
