from pathlib import Path

page = Path('frontend/app/pages/models/[id]/edit.vue')
source = page.read_text()
old = '''    <div class="flex flex-wrap items-start justify-between gap-5">
      <div class="min-w-0 flex-1">
'''
new = '''    <div class="flex flex-col gap-5 sm:flex-row sm:items-start sm:justify-between" data-testid="model-edit-header">
      <div class="w-full min-w-0 sm:flex-1">
'''
if source.count(old) != 1:
    raise SystemExit(f'Edit model header marker: expected one match, found {source.count(old)}')
source = source.replace(old, new, 1)
old = '''      <AppButton to="/models" intent="secondary">Back to Models</AppButton>
'''
new = '''      <div class="w-full sm:w-auto sm:shrink-0"><AppButton to="/models" intent="secondary">Back to Models</AppButton></div>
'''
if source.count(old) != 1:
    raise SystemExit(f'Edit model Back action marker: expected one match, found {source.count(old)}')
page.write_text(source.replace(old, new, 1))

unit = Path('frontend/test/model-edit-redesign-cleanup.nuxt.test.ts')
text = unit.read_text()
anchor = '''  it('keeps the two Model-only peer surfaces and update contract', () => {
'''
test = '''  it('stacks Back to Models below the header copy on narrow screens', () => {
    expect(source).toContain('flex flex-col gap-5 sm:flex-row sm:items-start sm:justify-between')
    expect(source).toContain('w-full sm:w-auto sm:shrink-0')
  })

'''
if 'stacks Back to Models below the header copy' not in text:
    if text.count(anchor) != 1:
        raise SystemExit(f'Edit model test anchor: expected one match, found {text.count(anchor)}')
    text = text.replace(anchor, test + anchor, 1)
unit.write_text(text)
