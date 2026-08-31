from pathlib import Path

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
count = source.count(old)
if count != 1:
    raise SystemExit(f'Instances header marker: expected one match, found {count}')
source = source.replace(old, new, 1)
old = """      <div class="flex flex-wrap items-center justify-end gap-2">
"""
new = """      <div class="flex w-full flex-wrap items-center justify-start gap-2 sm:w-auto sm:shrink-0 sm:justify-end">
"""
count = source.count(old)
if count != 1:
    raise SystemExit(f'Instances action group marker: expected one match, found {count}')
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
    # Reuse the file's existing node imports when present; otherwise add them.
    if "from 'node:fs'" not in text:
        text = "import { readFileSync } from 'node:fs'\nimport { resolve } from 'node:path'\n" + text
    text = text.replace(anchor, test + anchor, 1)
unit.write_text(text)
