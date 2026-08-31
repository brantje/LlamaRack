from pathlib import Path

path = Path('frontend/app/components/LlamaCppOptionsEditor.vue')
source = path.read_text()

replacements = [
    (
        "No overrides configured · inherited options available below",
        "No overrides configured · inheriting all values · inherited options available below",
        "empty override summary",
    ),
    (
        "Inherited value: <code>{{ effectiveValue(option.key) }}</code>",
        "Effective inherited value: <code>{{ effectiveValue(option.key) }}</code>",
        "inherited value label",
    ),
    (
        ">Override</AppButton>",
        ">Override here</AppButton>",
        "inherited override action label",
    ),
]

for old, new, label in replacements:
    count = source.count(old)
    if count != 1:
        raise SystemExit(f'{label}: expected one match, found {count}')
    source = source.replace(old, new, 1)

path.write_text(source)

e2e = Path('frontend/e2e/redesign-screenshots.spec.ts')
text = e2e.read_text()
old = """    { key: 'parallel', value_hint: 'N', description: 'Number of parallel sequences', kind: 'number' },
    { key: 'flash-attn', description: 'Enable Flash Attention', kind: 'boolean' }
"""
new = """    { key: 'parallel', value_hint: 'N', description: 'Number of parallel sequences', kind: 'number' },
    { key: 'flash-attn', description: 'Enable Flash Attention', kind: 'boolean' },
    { key: 'threads', value_hint: 'N', description: 'CPU worker threads', kind: 'number' }
"""
count = text.count(old)
if count != 1:
    raise SystemExit(f'inherited visual fixture: expected one match, found {count}')
e2e.write_text(text.replace(old, new, 1))
