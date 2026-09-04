import { describe, expect, it } from 'vitest'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'

const page = readFileSync(resolve(process.cwd(), 'app/pages/admin/authentication.vue'), 'utf8')
const nav = readFileSync(resolve(process.cwd(), 'app/components/navigation/AdminSidebar.vue'), 'utf8')

describe('Authentication redesign cleanup', () => {
  it('keeps Authentication in the Access group of the Administration secondary navigation', () => {
    const access = nav.indexOf("label: 'Access'")
    const users = nav.indexOf("label: 'Users'")
    const serviceAccounts = nav.indexOf("label: 'Service accounts'")
    const authentication = nav.indexOf("label: 'Authentication'")
    const llamacpp = nav.indexOf("label: 'llama.cpp'")

    expect(access).toBeGreaterThanOrEqual(0)
    expect(users).toBeGreaterThan(access)
    expect(serviceAccounts).toBeGreaterThan(users)
    expect(authentication).toBeGreaterThan(serviceAccounts)
    expect(llamacpp).toBeGreaterThan(authentication)
    expect(nav).not.toContain("description: 'Local login and OIDC providers'")
    expect(nav).toContain("to: '/admin/authentication'")
  })

  it('uses semantic notes and provider status tags without nested sign-in Frames', () => {
    expect(page).toContain('data-testid="authentication-error"')
    expect(page).toContain('<StatusTag variant="failed">Authentication error</StatusTag>')
    expect(page).toContain('data-testid="authentication-success"')
    expect(page).toContain('<StatusTag variant="ready">Updated</StatusTag>')
    expect(page).toContain('data-testid="authentication-auto-link-note"')
    expect(page).toContain('<StatusTag variant="pending">Explicit linking required</StatusTag>')
    expect(page).toContain("provider.enabled ? 'ready' : 'neutral'")
    expect(page).toContain("provider.last_test_succeeded ? 'ready' : 'pending'")
    expect(page).toContain('intent="primary" :loading="savingSettings" :disabled="!settings"')
    expect(page).not.toContain('border-[var(--accent-800)]')
  })

  it('keeps modal hierarchy, callback rendering and stored secrets source-safe', () => {
    expect(page).toContain('<AppButton intent="secondary" @click="providerModalOpen = false">Cancel</AppButton>')
    expect(page).toContain("<AppButton intent=\"secondary\" :loading=\"providerBusy === 'draft-test'\" @click=\"testProviderForm\">Test configuration</AppButton>")
    expect(page).toContain('<AppButton intent="primary" :loading="providerBusy === (editingProvider?.id || \'new\')" @click="saveProvider">Save provider</AppButton>')
    expect(page).toContain("`${base}/api/v1/auth/oidc/${encodeURIComponent(provider.id)}/callback`")
    expect(page).toContain("client_secret: ''")
    expect(page).toContain('...(providerForm.client_secret ? { client_secret: providerForm.client_secret } : {})')
    expect(page).toContain("editingProvider?.secret_configured ? 'Replace client secret' : 'Client secret'")
    expect(page).not.toContain('editingProvider.client_secret')
  })
})
