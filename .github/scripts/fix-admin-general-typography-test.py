from pathlib import Path

path = Path('frontend/test/admin-machine-typography.nuxt.test.ts')
source = path.read_text()
old = "    expect(content).toContain('font-mono text-[11.5px] font-normal')\n"
new = "    expect(content).toContain('font-mono text-xs font-normal')\n    expect(content).toContain('source: {{ source }}')\n"
if source.count(old) != 1:
    raise SystemExit(f'expected one provenance typography assertion, found {source.count(old)}')
path.write_text(source.replace(old, new, 1))
