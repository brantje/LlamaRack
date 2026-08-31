from pathlib import Path

page = Path('frontend/app/pages/models/new.vue')
source = page.read_text()
old = '''    <div class="flex flex-wrap items-start justify-between gap-4" data-testid="model-add-header">
      <UPageHeader
        class="min-w-0 flex-1"
'''
new = '''    <div class="flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between" data-testid="model-add-header">
      <UPageHeader
        class="w-full min-w-0 sm:flex-1"
'''
count = source.count(old)
if count != 1:
    raise SystemExit(f'Add model header marker: expected one match, found {count}')
source = source.replace(old, new, 1)
old = '''      <AppButton :to="remoteMode ? '/models/discover' : '/models'" intent="secondary">{{ remoteMode ? 'Back to Discover' : 'Back to models' }}</AppButton>
'''
new = '''      <div class="w-full sm:w-auto sm:shrink-0"><AppButton :to="remoteMode ? '/models/discover' : '/models'" intent="secondary">{{ remoteMode ? 'Back to Discover' : 'Back to models' }}</AppButton></div>
'''
count = source.count(old)
if count != 1:
    raise SystemExit(f'Add model Back action marker: expected one match, found {count}')
page.write_text(source.replace(old, new, 1))

existing = Path('frontend/test/model-add-redesign.nuxt.test.ts')
text = existing.read_text()
old = "    expect(wrapper.get('[data-testid=\"model-add-header\"]').classes()).toContain('flex-wrap')\n"
new = "    expect(wrapper.get('[data-testid=\"model-add-header\"]').classes()).toEqual(expect.arrayContaining(['flex', 'flex-col', 'gap-4', 'sm:flex-row']))\n"
if text.count(old) != 1:
    raise SystemExit(f'Add model existing header assertion: expected one match, found {text.count(old)}')
existing.write_text(text.replace(old, new, 1))

unit = Path('frontend/test/model-add-mobile-header.nuxt.test.ts')
unit.write_text('''import { readFileSync } from 'node:fs'\nimport { resolve } from 'node:path'\nimport { describe, expect, it } from 'vitest'\n\nconst source = readFileSync(resolve(process.cwd(), 'app/pages/models/new.vue'), 'utf8')\n\ndescribe('Add model responsive header', () => {\n  it('stacks Back below the intro before narrow viewports can squeeze the copy', () => {\n    expect(source).toContain('data-testid="model-add-header"')\n    expect(source).toContain('flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between')\n    expect(source).toContain('class="w-full min-w-0 sm:flex-1"')\n    expect(source).toContain('w-full sm:w-auto sm:shrink-0')\n  })\n})\n''')
