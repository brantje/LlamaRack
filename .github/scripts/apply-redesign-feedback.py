from pathlib import Path


def replace(path: str, old: str, new: str, count: int = 1) -> None:
    file = Path(path)
    source = file.read_text()
    found = source.count(old)
    if found != count:
        raise SystemExit(f"{path}: expected {count} occurrence(s), found {found}: {old[:100]!r}")
    file.write_text(source.replace(old, new, count))


logs = "frontend/app/pages/logs/index.vue"
replace(
    logs,
    """const liveState = computed(() => {
  if (!liveStreamingEnabled.value) return { label: 'Live off', color: 'neutral' as const }
  if (!manager.runtimeEventsConnected.value) return { label: 'Disconnected', color: 'neutral' as const }
  if (offset.value > 0) return { label: 'Live paused on older page', color: 'warning' as const }
  return { label: 'Live', color: 'success' as const }
})""",
    """const liveState = computed(() => {
  if (!liveStreamingEnabled.value) return { label: 'Live off', color: 'neutral' as const }
  if (!manager.runtimeEventsConnected.value) return { label: 'Disconnected', color: 'neutral' as const }
  if (offset.value > 0) return { label: 'Live paused on older page', color: 'warning' as const }
  return { label: 'Live', color: 'success' as const }
})
const liveAction = computed(() => {
  if (!liveStreamingEnabled.value) return { label: 'Enable live', icon: 'i-lucide-play' }
  if (!manager.runtimeEventsConnected.value) return { label: 'Reconnect', icon: 'i-lucide-refresh-cw' }
  if (offset.value > 0) return { label: 'Return to live', icon: 'i-lucide-arrow-up' }
  return { label: 'Pause live', icon: 'i-lucide-pause' }
})""",
)
replace(
    logs,
    """async function toggleLiveStreaming() {
  liveStreamingEnabled.value = !liveStreamingEnabled.value
  if (liveStreamingEnabled.value && routeReady.value && manager.user.value && offset.value === 0) await loadRequests()
}""",
    """async function toggleLiveStreaming() {
  liveStreamingEnabled.value = !liveStreamingEnabled.value
  if (liveStreamingEnabled.value && routeReady.value && manager.user.value && offset.value === 0) await loadRequests()
}
async function handleLiveAction() {
  if (!liveStreamingEnabled.value) {
    await toggleLiveStreaming()
    return
  }
  if (!manager.runtimeEventsConnected.value) {
    await manager.connectRuntimeEvents()
    return
  }
  if (offset.value > 0) {
    offset.value = 0
    await loadRequests()
    return
  }
  await toggleLiveStreaming()
}""",
)
replace(
    logs,
    """      <div class=\"flex flex-wrap items-center justify-end gap-2\">
        <StatusTag data-testid=\"request-logs-live-state\" :variant=\"liveState.label === 'Live' ? 'ready' : liveState.label.includes('paused') ? 'pending' : 'neutral'\">{{ liveState.label }}</StatusTag>
        <AppButton data-testid=\"request-logs-live-toggle\" intent=\"secondary\" :icon=\"liveStreamingEnabled ? 'i-lucide-pause' : 'i-lucide-play'\" @click=\"toggleLiveStreaming\">{{ liveStreamingEnabled ? 'Pause live' : 'Enable live' }}</AppButton>""",
    """      <div class=\"flex w-full flex-wrap items-center justify-start gap-2 sm:w-auto sm:justify-end\">
        <StatusTag data-testid=\"request-logs-live-state\" :variant=\"liveState.label === 'Live' ? 'ready' : liveState.label.includes('paused') ? 'pending' : 'neutral'\">{{ liveState.label }}</StatusTag>
        <AppButton data-testid=\"request-logs-live-toggle\" intent=\"secondary\" :icon=\"liveAction.icon\" @click=\"handleLiveAction\">{{ liveAction.label }}</AppButton>""",
)
replace(
    logs,
    """      <div v-else class=\"overflow-x-auto\">
        <UTable :data=\"displayRequests\" :columns=\"columns\" class=\"min-w-[1580px]\">""",
    """      <div v-else class=\"overflow-x-auto\" role=\"region\" aria-label=\"Request history table. Scroll horizontally to view all columns on small screens.\" tabindex=\"0\">
        <UTable :data=\"displayRequests\" :columns=\"columns\" class=\"min-w-[1580px]\">""",
)

instance = "frontend/app/pages/instances/[id]/detail.vue"
replace(instance, "const historyLoading = ref(false)\nconst pending = ref('')", "const historyLoading = ref(false)\nconst historyError = ref('')\nconst pending = ref('')")
replace(
    instance,
    """  const sequence = ++historySequence
  historyLoading.value = true
  try {""",
    """  const sequence = ++historySequence
  historyLoading.value = true
  historyError.value = ''
  try {""",
)
replace(
    instance,
    """  } catch (value: any) {
    if (sequence === historySequence) error.value = value?.data?.error || value?.message || 'Unable to load Instance history'
  } finally {""",
    """  } catch (value: any) {
    if (sequence === historySequence) historyError.value = value?.data?.error || value?.message || 'Unable to load Instance history'
  } finally {""",
)
replace(
    instance,
    """      <div class=\"flex flex-wrap items-center justify-between gap-3\">
        <div><h2 class=\"text-base font-semibold\">Performance history</h2><p class=\"mt-1 text-xs text-[var(--neutral-700)]\">Server-bucketed history for this Instance only.</p></div>
        <div class=\"flex items-center gap-2\"><span v-if=\"historyLoading\" class=\"text-[10px] uppercase tracking-[.12em] text-[var(--neutral-700)]\">Refreshing</span><USelect v-model=\"selectedWindow\" data-testid=\"instance-detail-history-range\" aria-label=\"Instance history range\" :items=\"selectableRanges\" value-key=\"value\" label-key=\"label\" size=\"sm\" class=\"min-w-28\" /></div>
      </div>

      <section class=\"grid gap-4 md:grid-cols-2 xl:grid-cols-4\">""",
    """      <div class=\"flex flex-wrap items-center justify-between gap-3\">
        <div><h2 class=\"text-base font-semibold\">Performance history</h2><p class=\"mt-1 text-xs text-[var(--neutral-700)]\">Server-bucketed history for this Instance only.</p></div>
        <div class=\"flex items-center gap-2\"><span v-if=\"historyLoading\" class=\"text-[10px] uppercase tracking-[.12em] text-[var(--neutral-700)]\">Refreshing</span><USelect v-model=\"selectedWindow\" data-testid=\"instance-detail-history-range\" aria-label=\"Instance history range\" :items=\"selectableRanges\" value-key=\"value\" label-key=\"label\" size=\"sm\" class=\"min-w-28\" /></div>
      </div>

      <Frame v-if=\"historyError\" class=\"p-3\" data-testid=\"instance-detail-history-error\">
        <div class=\"flex flex-wrap items-center gap-2\">
          <StatusTag variant=\"failed\">Performance history unavailable</StatusTag>
          <p class=\"min-w-0 flex-1 text-xs text-muted\">{{ historyError }}</p>
          <AppButton intent=\"secondary\" size=\"xs\" :loading=\"historyLoading\" @click=\"loadHistory\">Retry history</AppButton>
        </div>
      </Frame>

      <section class=\"grid gap-4 md:grid-cols-2 xl:grid-cols-4\">""",
)

chart = "frontend/app/components/InstanceHistoryChart.vue"
replace(
    chart,
    """    <svg :viewBox=\"`0 0 ${width} ${height}`\" class=\"h-40 w-full overflow-visible\" role=\"img\" aria-label=\"History line chart\">""",
    """    <svg v-if=\"presentValues.length\" :viewBox=\"`0 0 ${width} ${height}`\" class=\"h-40 w-full overflow-visible\" role=\"img\" aria-label=\"History line chart\">""",
)
replace(
    chart,
    """    </svg>
    <p v-if=\"!presentValues.length\" class=\"text-center text-[11px] text-[var(--neutral-700)]\">No retained samples in this range.</p>""",
    """    </svg>
    <div v-else class=\"grid min-h-24 place-items-center border border-dashed border-[var(--color-divider)] px-3 py-4\">
      <p class=\"text-center text-[11px] text-[var(--neutral-800)]\">No retained samples in this range.</p>
    </div>""",
)

live_test = "frontend/test/request-logs-live.nuxt.test.ts"
replace(
    live_test,
    """    expect(wrapper.get('[data-testid=\"request-logs-live-state\"]').text()).toContain('paused')

    const callsBeforeLive""",
    """    expect(wrapper.get('[data-testid=\"request-logs-live-state\"]').text()).toContain('paused')
    expect(wrapper.get('[data-testid=\"request-logs-live-toggle\"]').text()).toContain('Return to live')

    const callsBeforeLive""",
)
replace(
    live_test,
    """    const previous = wrapper.findAll('button').find(button => button.text().trim() === 'Previous')
    await previous!.trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid=\"request-log-table\"]').text()).toContain('lcm_3')
    expect(wrapper.get('[data-testid=\"request-logs-live-state\"]').text()).toBe('Live')
    wrapper.unmount()""",
    """    await wrapper.get('[data-testid=\"request-logs-live-toggle\"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid=\"request-log-table\"]').text()).toContain('lcm_3')
    expect(wrapper.get('[data-testid=\"request-logs-live-state\"]').text()).toBe('Live')
    expect(wrapper.get('[data-testid=\"request-logs-live-toggle\"]').text()).toContain('Pause live')
    wrapper.unmount()""",
)
replace(
    live_test,
    """  it('waits for authentication before loading protected request history', async () => {""",
    """  it('offers reconnect instead of pause while the shared live stream is disconnected', async () => {
    const manager = resetManager()
    manager.runtimeEventsConnected.value = false
    mocks.request.mockResolvedValue({ items: [], has_more: false })

    const wrapper = await mountSuspended(LogsPage, { route: '/logs' })
    await flushPromises()
    expect(wrapper.get('[data-testid=\"request-logs-live-state\"]').text()).toBe('Disconnected')
    expect(wrapper.get('[data-testid=\"request-logs-live-toggle\"]').text()).toContain('Reconnect')
    expect(wrapper.get('[data-testid=\"request-logs-live-toggle\"]').text()).not.toContain('Pause live')
    wrapper.unmount()
  })

  it('waits for authentication before loading protected request history', async () => {""",
)

instance_test = "frontend/test/instance-detail-branches.nuxt.test.ts"
replace(
    instance_test,
    """    expect(wrapper.text()).toContain('history offline')

    const manager = useManager()""",
    """    expect(wrapper.text()).toContain('history offline')
    expect(wrapper.get('[data-testid=\"instance-detail-history-error\"]').text()).toContain('Performance history unavailable')
    expect(wrapper.get('[data-testid=\"instance-detail-summary\"]').text()).toContain('READY')
    expect(wrapper.find('[data-testid=\"instance-detail-error\"]').exists()).toBe(false)

    const manager = useManager()""",
)
replace(
    instance_test,
    """    expect(missing.text()).toContain('Instance “missing” was not found.')
  })""",
    """    expect(missing.text()).toContain('Instance “missing” was not found.')
    expect(missing.find('[data-testid=\"instance-detail-summary\"]').exists()).toBe(false)
    expect(missing.text()).not.toContain('READY')
  })""",
)

screenshots = "frontend/e2e/redesign-screenshots.spec.ts"
replace(
    screenshots,
    "request_id: 'req_a1b2c3', accepted_at:",
    "request_id: 'req_a1b2c3', trace_id: 'trace_fixture', session_id: 'session_fixture', session_total_count: 2, model_name: 'Qwen3 8B', call_type: 'chat_completion', request_body: '{\\\"messages\\\":[{\\\"role\\\":\\\"user\\\",\\\"content\\\":\\\"Explain KV cache reuse\\\"}]}', response_body: '{\\\"choices\\\":[{\\\"message\\\":{\\\"role\\\":\\\"assistant\\\",\\\"content\\\":\\\"Reuse avoids repeated prompt evaluation.\\\"}}]}', accepted_at:",
)
replace(
    screenshots,
    "request_id: 'req_d4e5f6', accepted_at:",
    "request_id: 'req_d4e5f6', trace_id: 'trace_fixture', session_id: 'session_fixture', session_total_count: 2, model_name: 'Gemma 3 12B', call_type: 'response', accepted_at:",
)
replace(
    screenshots,
    "if (pathname === '/api/v1/observability/timeseries') return { metric: 'fixture', bucket_seconds: 60, items: [] }",
    "if (pathname === '/api/v1/observability/timeseries') return { metric: 'fixture', bucket_seconds: 60, items: [{ timestamp: now - 120_000, value: 18 }, { timestamp: now - 60_000, value: 31 }, { timestamp: now, value: 24 }] }",
)
replace(
    screenshots,
    "  ['request-logs', '/logs'],",
    "  ['request-logs', '/logs'],\n  ['request-logs-trace', '/logs?trace_id=trace_fixture'],\n  ['request-log-detail', '/logs?request_id=req_a1b2c3&session_id=session_fixture'],",
)
