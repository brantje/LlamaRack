from pathlib import Path

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
count = source.count(old)
if count != 1:
    raise SystemExit(f'Instance form header marker: expected one match, found {count}')
source = source.replace(old, new, 1)
old = """      <AppButton to="/instances" intent="secondary">Back to Instances</AppButton>
"""
new = """      <div class="w-full sm:w-auto sm:shrink-0"><AppButton to="/instances" intent="secondary">Back to Instances</AppButton></div>
"""
count = source.count(old)
if count != 1:
    raise SystemExit(f'Instance form Back action marker: expected one match, found {count}')
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
