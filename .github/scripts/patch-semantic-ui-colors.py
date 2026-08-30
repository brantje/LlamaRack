from pathlib import Path


def replace_once(path: str, old: str, new: str, label: str) -> None:
    file = Path(path)
    source = file.read_text()
    if new in source:
        return
    count = source.count(old)
    if count != 1:
        raise SystemExit(f'{label}: expected one match, found {count}')
    file.write_text(source.replace(old, new, 1))


replace_once(
    'frontend/app/app.config.ts',
    """      success: 'accent',
      info: 'accent',
      warning: 'accent',
      error: 'accent',""",
    """      success: 'accent',
      info: 'accent',
      warning: 'neutral',
      error: 'danger',""",
    'Nuxt semantic color aliases',
)

replace_once(
    'frontend/app/assets/css/main.css',
    """  --color-accent-950: var(--accent-900);

  --color-neutral-50:""",
    """  --color-accent-950: var(--accent-900);

  /* Keep Nuxt's error semantic theme-owned without introducing a rainbow status palette. */
  --color-danger-50: var(--danger-100);
  --color-danger-100: var(--danger-100);
  --color-danger-200: color-mix(in srgb, var(--danger-100) 70%, var(--color-danger));
  --color-danger-300: color-mix(in srgb, var(--danger-100) 45%, var(--color-danger));
  --color-danger-400: color-mix(in srgb, var(--danger-100) 20%, var(--color-danger));
  --color-danger-500: var(--color-danger);
  --color-danger-600: color-mix(in srgb, var(--color-danger) 75%, var(--danger-700));
  --color-danger-700: var(--danger-700);
  --color-danger-800: var(--danger-700);
  --color-danger-900: var(--danger-700);
  --color-danger-950: var(--danger-700);

  --color-neutral-50:""",
    'theme-owned danger Tailwind palette',
)

path = Path('frontend/test/design-rules.nuxt.test.ts')
source = path.read_text()
marker = """  it('keeps destructive action tone separate from button priority', () => {"""
addition = """  it('does not alias warning and error semantics to the primary accent', () => {
    const config = readFileSync(resolve(process.cwd(), 'app/app.config.ts'), 'utf8')
    const css = readFileSync(resolve(process.cwd(), 'app/assets/css/main.css'), 'utf8')
    expect(config).toContain("warning: 'neutral'")
    expect(config).toContain("error: 'danger'")
    expect(config).not.toMatch(/warning:\s*'accent'[\s\S]*error:\s*'accent'/)
    expect(css).toContain('--color-danger-500: var(--color-danger)')
    expect(css).toContain('--color-danger-700: var(--danger-700)')
  })

"""
if addition not in source:
    if source.count(marker) != 1:
        raise SystemExit(f'semantic color rule insertion: expected one marker, found {source.count(marker)}')
    path.write_text(source.replace(marker, addition + marker, 1))
