from pathlib import Path

path = Path('frontend/test/phase10-branch-coverage.nuxt.test.ts')
source = path.read_text()

helper_marker = """function button(wrapper: any, text: string) {
  const found = wrapper.findAll('button').find((candidate: any) => candidate.text().trim() === text)
  if (!found) throw new Error(`Missing button: ${text}`)
  return found
}
"""
if 'async function confirmHuggingFaceRemoval()' not in source:
    count = source.count(helper_marker)
    if count != 1:
        raise SystemExit(f'phase10 button helper: expected one match, found {count}')
    source = source.replace(
        helper_marker,
        helper_marker + """
async function confirmHuggingFaceRemoval() {
  await flushPromises()
  const control = [...document.body.querySelectorAll<HTMLButtonElement>('[data-testid="confirmation-confirm"]')].at(-1)
  if (!control) throw new Error('Missing Hugging Face removal confirmation button')
  control.click()
  await flushPromises()
}
""",
        1,
    )

old = """    mode = 'data-error'
    await button(wrapper, 'Remove').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('hf denied')
    mode = 'configured'
    await button(wrapper, 'Remove').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('Not configured')
"""
new = """    mode = 'data-error'
    await button(wrapper, 'Remove').trigger('click')
    await confirmHuggingFaceRemoval()
    expect(wrapper.text()).toContain('hf denied')
    mode = 'configured'
    await button(wrapper, 'Remove').trigger('click')
    await confirmHuggingFaceRemoval()
    expect(wrapper.text()).toContain('Not configured')
"""
count = source.count(old)
if count != 1:
    raise SystemExit(f'phase10 Hugging Face remove flow: expected one match, found {count}')
path.write_text(source.replace(old, new, 1))
