from pathlib import Path

page = Path('frontend/app/pages/downloads.vue')
source = page.read_text()
old = """    <div class="flex flex-wrap items-start justify-between gap-5">
      <div class="min-w-0 flex-1">
"""
new = """    <div class="flex flex-col gap-5 sm:flex-row sm:items-start sm:justify-between" data-testid="downloads-header">
      <div class="w-full min-w-0 sm:flex-1">
"""
count = source.count(old)
if count != 1:
    raise SystemExit(f'Downloads header marker: expected one match, found {count}')
source = source.replace(old, new, 1)
old = """        </p>
      </div>
      <div class="flex flex-wrap items-center justify-end gap-2">
        <StatusTag :variant="liveUpdates ? 'ready' : 'pending'">"""
new = """        </p>
      </div>
      <div class="flex w-full flex-wrap items-center justify-start gap-2 sm:w-auto sm:shrink-0 sm:justify-end">
        <StatusTag :variant="liveUpdates ? 'ready' : 'pending'">"""
count = source.count(old)
if count != 1:
    raise SystemExit(f'Downloads header action marker: expected one match, found {count}')
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
