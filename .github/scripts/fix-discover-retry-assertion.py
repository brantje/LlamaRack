from pathlib import Path

path = Path('frontend/e2e/redesign-screenshots.spec.ts')
source = path.read_text()
old = "  await expect(page.getByRole('heading', { name: 'Qwen/Qwen3-8B-GGUF' })).toBeVisible()"
new = "  await expect(page.locator('[data-testid=\"discover-repository-header\"]').getByRole('heading', { name: 'Qwen/Qwen3-8B-GGUF' })).toBeVisible()"
count = source.count(old)
if count != 1:
    raise SystemExit(f'unexpected Discover retry assertion marker count: {count}')
path.write_text(source.replace(old, new, 1))
