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
]

for old, new, label in replacements:
    count = source.count(old)
    if count != 1:
        raise SystemExit(f'{label}: expected one match, found {count}')
    source = source.replace(old, new, 1)

path.write_text(source)
