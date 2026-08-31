from pathlib import Path

# Instances
page = Path('frontend/app/pages/instances/index.vue')
source = page.read_text()
old = """    <div class="flex flex-wrap items-start justify-between gap-4">
      <UPageHeader
        class="min-w-0 flex-1"
"""
new = """    <div class="flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between" data-testid="instances-header">
      <UPageHeader
        class="w-full min-w-0 sm:flex-1"
"""
if source.count(old) != 1:
    raise SystemExit(f'Instances header marker: expected one match, found {source.count(old)}')
source = source.replace(old, new, 1)
old = """      <div class="flex flex-wrap items-center justify-end gap-2">
"""
new = """      <div class="flex w-full flex-wrap items-center justify-start gap-2 sm:w-auto sm:shrink-0 sm:justify-end">
"""
if source.count(old) != 1:
    raise SystemExit(f'Instances action group marker: expected one match, found {source.count(old)}')
page.write_text(source.replace(old, new, 1))

unit = Path('frontend/test/instances-redesign-cleanup.nuxt.test.ts')
text = unit.read_text()
anchor = """describe('Instances redesign cleanup', () => {
"""
test = """describe('Instances responsive header', () => {
  it('stacks page actions below the header copy on narrow screens', () => {
    const source = readFileSync(resolve(process.cwd(), 'app/pages/instances/index.vue'), 'utf8')
    expect(source).toContain('flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between')
    expect(source).toContain('w-full flex-wrap items-center justify-start gap-2 sm:w-auto sm:shrink-0 sm:justify-end')
  })
})

"""
if 'Instances responsive header' not in text:
    if text.count(anchor) != 1:
        raise SystemExit(f'Instances test anchor: expected one match, found {text.count(anchor)}')
    if "from 'node:fs'" not in text:
        text = "import { readFileSync } from 'node:fs'\nimport { resolve } from 'node:path'\n" + text
    text = text.replace(anchor, test + anchor, 1)
unit.write_text(text)

# Downloads
page = Path('frontend/app/pages/downloads.vue')
source = page.read_text()
old = """    <div class="flex flex-wrap items-start justify-between gap-5">
      <div class="min-w-0 flex-1">
"""
new = """    <div class="flex flex-col gap-5 sm:flex-row sm:items-start sm:justify-between" data-testid="downloads-header">
      <div class="w-full min-w-0 sm:flex-1">
"""
if source.count(old) != 1:
    raise SystemExit(f'Downloads header marker: expected one match, found {source.count(old)}')
source = source.replace(old, new, 1)
old = """        </p>
      </div>
      <div class="flex flex-wrap items-center justify-end gap-2">
        <StatusTag :variant="liveUpdates ? 'ready' : 'pending'">"""
new = """        </p>
      </div>
      <div class="flex w-full flex-wrap items-center justify-start gap-2 sm:w-auto sm:shrink-0 sm:justify-end">
        <StatusTag :variant="liveUpdates ? 'ready' : 'pending'">"""
if source.count(old) != 1:
    raise SystemExit(f'Downloads header action marker: expected one match, found {source.count(old)}')
page.write_text(source.replace(old, new, 1))

unit = Path('frontend/test/downloads-redesign-cleanup.nuxt.test.ts')
text = unit.read_text()
anchor = """describe('Downloads redesign cleanup', () => {
"""
test = """describe('Downloads responsive header', () => {
  it('stacks download utilities below the header copy on narrow screens', () => {
    expect(source).toContain('flex flex-col gap-5 sm:flex-row sm:items-start sm:justify-between')
    expect(source).toContain('w-full flex-wrap items-center justify-start gap-2 sm:w-auto sm:shrink-0 sm:justify-end')
  })
})

"""
if 'Downloads responsive header' not in text:
    if text.count(anchor) != 1:
        raise SystemExit(f'Downloads test anchor: expected one match, found {text.count(anchor)}')
    text = text.replace(anchor, test + anchor, 1)
unit.write_text(text)

# Shared New/Edit Instance form
page = Path('frontend/app/components/InstanceForm.vue')
source = page.read_text()
old = """    <div class="flex flex-wrap items-start justify-between gap-4">
      <UPageHeader
        class="min-w-0 flex-1"
"""
new = """    <div class="flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between" data-testid="instance-form-header">
      <UPageHeader
        class="w-full min-w-0 sm:flex-1"
"""
if source.count(old) != 1:
    raise SystemExit(f'Instance form header marker: expected one match, found {source.count(old)}')
source = source.replace(old, new, 1)
old = """      <AppButton to="/instances" intent="secondary">Back to Instances</AppButton>
"""
new = """      <div class="w-full sm:w-auto sm:shrink-0"><AppButton to="/instances" intent="secondary">Back to Instances</AppButton></div>
"""
if source.count(old) != 1:
    raise SystemExit(f'Instance form Back action marker: expected one match, found {source.count(old)}')
page.write_text(source.replace(old, new, 1))

unit = Path('frontend/test/instance-form-redesign-cleanup.nuxt.test.ts')
text = unit.read_text()
anchor = """  it('retains the issue-required identity and placement hooks', () => {
"""
test = """  it('stacks the Back action below Instance form header copy on narrow screens', () => {
    expect(formSource).toContain('flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between')
    expect(formSource).toContain('w-full sm:w-auto sm:shrink-0')
  })

"""
if 'stacks the Back action below Instance form header copy' not in text:
    if text.count(anchor) != 1:
        raise SystemExit(f'Instance form test anchor: expected one match, found {text.count(anchor)}')
    text = text.replace(anchor, test + anchor, 1)
unit.write_text(text)
