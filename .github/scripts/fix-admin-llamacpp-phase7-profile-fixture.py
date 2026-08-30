from pathlib import Path

path = Path('frontend/test/phase7-components.nuxt.test.ts')
source = path.read_text()
old = """        return {
          profile: { options: editorProfile.options },
          effective: { global: { 'ctx-size': '4096' }, model: {}, instance: {}, values: {}, sources: {} }
        }
"""
new = """        return {
          profile: { ...editorProfile, version: undefined },
          effective: { global: { 'ctx-size': '4096' }, model: {}, instance: {}, values: {}, sources: {} }
        }
"""
count = source.count(old)
if count != 1:
    raise SystemExit(f'phase7 current-profile fixture: expected one match, found {count}')
path.write_text(source.replace(old, new, 1))
