from pathlib import Path

path = Path('frontend/e2e/redesign-screenshots.spec.ts')
source = path.read_text()


def replace_once(old: str, new: str, label: str) -> None:
    global source
    count = source.count(old)
    if count != 1:
        raise SystemExit(f'{label}: expected one match, found {count}')
    source = source.replace(old, new, 1)

replace_once(
    "  if (pathname === '/api/v1/observability/requests') return { items: requests, next_cursor: '' }\n",
    """  if (pathname === '/api/v1/observability/requests/req_a1b2c3') return requests[0]
  if (pathname === '/api/v1/observability/requests/req_d4e5f6') return requests[1]
  if (pathname === '/api/v1/observability/requests/req_failure_fixture') return {
    id: 502, request_id: 'req_failure_fixture', trace_id: 'trace_failure_fixture', model_name: 'Qwen3 8B', call_type: 'chat_completion',
    request_body: '{\"messages\":[{\"role\":\"user\",\"content\":\"Trigger a representative failure\"}]}',
    response_body: '{\"error\":{\"message\":\"llama-server representative upstream failure\"}}',
    accepted_at: now - 8_000, started_at: now - 7_900, finished_at: now - 7_200, instance_id: 'qwen3-primary',
    endpoint: '/v1/chat/completions', api_key: { id: 'key-default', name: 'Open WebUI', prefix: 'lcm_sk_ab12' }, streaming: false,
    status_code: 503, result: 'error', error: 'llama-server representative upstream failure', duration_ms: 700, ttft_ms: 0,
    prompt_tokens: 42, generated_tokens: 0, total_tokens: 42, queue_duration_ms: 11, load_duration_ms: 0, autoloaded: false
  }
  if (pathname === '/api/v1/observability/requests') return { items: requests, next_cursor: '' }
""",
    'request detail fixtures',
)

anchor = "\ntest('downloads lifecycle and files screenshot', async ({ page }, testInfo) => {\n"
addition = """

test('request log JSON payload screenshot', async ({ page }, testInfo) => {
  await page.goto('/logs?request_id=req_a1b2c3&session_id=session_fixture', { waitUntil: 'domcontentloaded' })
  await waitForManagerPanel(page)
  await expect(page.locator('[data-testid="request-detail-content"]')).toBeVisible()
  await page.getByRole('button', { name: 'JSON', exact: true }).click()
  await expect(page.locator('[data-testid="request-detail-content"]')).toContainText('messages')
  await page.screenshot({ path: `artifacts/ux-screenshots/${testInfo.project.name}/request-log-detail-json.png`, fullPage: true, animations: 'disabled' })
})


test('request log metadata-only detail screenshot', async ({ page }, testInfo) => {
  await page.goto('/logs?request_id=req_d4e5f6&session_id=session_fixture', { waitUntil: 'domcontentloaded' })
  await waitForManagerPanel(page)
  await expect(page.locator('[data-testid="request-detail-content"]')).toContainText('Content not recorded')
  await expect(page.locator('[data-testid="request-detail-content"]')).toContainText('metadata-only logging')
  await page.screenshot({ path: `artifacts/ux-screenshots/${testInfo.project.name}/request-log-detail-metadata-only.png`, fullPage: true, animations: 'disabled' })
})


test('request log failed detail screenshot', async ({ page }, testInfo) => {
  await page.goto('/logs?request_id=req_failure_fixture', { waitUntil: 'domcontentloaded' })
  await waitForManagerPanel(page)
  await expect(page.locator('[data-testid="request-failure-banner"]')).toContainText('Request Failed')
  await expect(page.locator('[data-testid="request-failure-banner"]')).toContainText('llama-server representative upstream failure')
  await page.screenshot({ path: `artifacts/ux-screenshots/${testInfo.project.name}/request-log-detail-failure.png`, fullPage: true, animations: 'disabled' })
})
"""
replace_once(anchor, addition + anchor, 'request log detail visual states')
path.write_text(source)
