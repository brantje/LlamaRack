from pathlib import Path

page = Path('frontend/app/pages/admin/users.vue')
source = page.read_text()
old = """            <AppButton intent="ghost" size="xs" @click="toggleUser(row.original)">{{ row.original.enabled ? 'Disable' : 'Enable' }}</AppButton>
"""
new = """            <AppButton intent="ghost" :tone="row.original.enabled ? 'destructive' : 'default'" size="xs" @click="toggleUser(row.original)">{{ row.original.enabled ? 'Disable' : 'Enable' }}</AppButton>
"""
count = source.count(old)
if count != 1:
    raise SystemExit(f'users toggle tone marker: expected one match, found {count}')
page.write_text(source.replace(old, new, 1))

unit = Path('frontend/test/phase10-admin.nuxt.test.ts')
text = unit.read_text()
marker = """    expect(wrapper.text()).toContain('Inference API keys are managed separately under API')
"""
addition = marker + """    const operatorRow = tableRow(wrapper, 'operator')
    expect(rowButton(operatorRow, 'Disable').attributes('class')).toContain('text-[var(--danger-700)]')
"""
if text.count(marker) != 1:
    raise SystemExit(f'users unit marker: expected one match, found {text.count(marker)}')
unit.write_text(text.replace(marker, addition, 1))

e2e = Path('frontend/e2e/redesign-screenshots.spec.ts')
text = e2e.read_text()

marker = """  if (pathname === '/api/v1/me/identities') return profileIdentities
"""
fixture = marker + """  if (pathname === '/api/v1/users' && method === 'GET') return [
    { id: 1, username: 'admin', enabled: true, bootstrap_admin: true, created_at: nowSeconds - 86400 * 120, last_login_at: nowSeconds - 300 },
    { id: 2, username: 'operator', enabled: false, bootstrap_admin: false, created_at: nowSeconds - 86400 * 21, last_login_at: nowSeconds - 86400 * 2 }
  ]
"""
if text.count(marker) != 1:
    raise SystemExit(f'users fixture marker: expected one match, found {text.count(marker)}')
text = text.replace(marker, fixture, 1)

anchor = "\ntest('downloads lifecycle and files screenshot', async ({ page }, testInfo) => {\n"
visual = """

test('Administration users create and reset modal screenshots', async ({ page }, testInfo) => {
  await page.goto('/admin/users', { waitUntil: 'domcontentloaded' })
  await waitForManagerPanel(page)
  const table = page.locator('[data-testid="admin-users-table"]')
  await expect(table).toContainText('bootstrap admin')
  await expect(table).toContainText('Disabled')

  await page.getByRole('button', { name: 'Add user' }).click()
  await expect(page.getByRole('dialog')).toContainText('Add user')
  await page.screenshot({ path: `artifacts/ux-screenshots/${testInfo.project.name}/admin-users-add.png`, fullPage: true, animations: 'disabled' })
  await page.getByRole('dialog').getByRole('button', { name: 'Cancel' }).click()

  await table.getByRole('button', { name: 'Reset password' }).first().click()
  await expect(page.getByRole('dialog')).toContainText('Reset password for admin')
  await expect(page.getByRole('dialog')).toContainText('revokes all sessions')
  await page.screenshot({ path: `artifacts/ux-screenshots/${testInfo.project.name}/admin-users-reset-password.png`, fullPage: true, animations: 'disabled' })
  await page.getByRole('dialog').getByRole('button', { name: 'Cancel' }).click()
})


test('Administration users disable and enable confirmation screenshots', async ({ page }, testInfo) => {
  await page.goto('/admin/users', { waitUntil: 'domcontentloaded' })
  await waitForManagerPanel(page)
  const table = page.locator('[data-testid="admin-users-table"]')
  await expect(table).toContainText('operator')

  await table.getByRole('button', { name: 'Disable' }).click()
  await expect(page.locator('[data-testid="confirmation-confirm"]')).toContainText('Disable user')
  await expect(page.getByText('All of that user’s active sessions will be revoked.', { exact: false })).toBeVisible()
  await page.screenshot({ path: `artifacts/ux-screenshots/${testInfo.project.name}/admin-users-disable-confirmation.png`, fullPage: true, animations: 'disabled' })
  await page.locator('[data-testid="confirmation-cancel"]').click()

  await table.getByRole('button', { name: 'Enable' }).click()
  await expect(page.locator('[data-testid="confirmation-confirm"]')).toContainText('Enable user')
  await page.screenshot({ path: `artifacts/ux-screenshots/${testInfo.project.name}/admin-users-enable-confirmation.png`, fullPage: true, animations: 'disabled' })
  await page.locator('[data-testid="confirmation-cancel"]').click()
})
"""
if text.count(anchor) != 1:
    raise SystemExit(f'users visual anchor: expected one match, found {text.count(anchor)}')
e2e.write_text(text.replace(anchor, visual + anchor, 1))
