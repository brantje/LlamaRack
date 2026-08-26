# AGENTS.md

## Frontend UI: component-first is mandatory

For frontend work, **Nuxt UI components are the first choice**. Before creating markup, a local component, or custom styling, check the Nuxt UI component catalog: https://ui.nuxt.com/docs/components

Use this implementation priority, in order:

1. **Nuxt UI component** when a suitable component exists.
2. **Nuxt UI props, variants, slots and theme configuration** to adapt the component.
3. **Tailwind utility classes** for page-specific layout and composition that Nuxt UI does not provide.
4. **Shared Tailwind/Nuxt UI theme tokens** for reusable visual values.
5. **Custom CSS only as a last resort** when Nuxt UI and Tailwind cannot express the requirement. Keep exceptions narrow and document why they are necessary.

Do not rebuild a Nuxt UI primitive with custom HTML/CSS. Do not prefer an existing repository-specific CSS class over a Nuxt UI component merely because the class already exists. When touching legacy frontend UI, migrate the touched surface toward Nuxt UI + Tailwind rather than extending the legacy CSS system.

The target styling model is Tailwind + Nuxt UI. Legacy selector-driven component CSS must be removed as surfaces are migrated. A CSS entry point may exist for required Tailwind/Nuxt UI imports and narrowly scoped theme/token declarations, but it must not become a bespoke component library or a collection of `@apply`-based replacements for Nuxt UI components.

Preserve the existing llamacpp-manager theme unless a task explicitly requests a redesign. Prefer semantic Nuxt UI/Tailwind theme configuration over repeated raw color literals.

**Never put cards inside cards.** Do not nest `UCard`, `UPageCard`, or card-like bordered/elevated containers inside another card. When content needs hierarchy within a card, use sections, separators, tables, alerts, disclosure components, spacing, or typography instead of another card surface. Components with surfaced defaults must also stay flat when nested: for example, `UEmpty` defaults to an outlined surface, so use `variant="naked"` whenever `UEmpty` is rendered inside a card.

## Nuxt routing mount invariant — hard rule

`[NUXT_E4011] Your project has pages but the <NuxtPage /> component has not been used` is a **hard regression and must never be shipped or ignored**.

- `frontend/app/app.vue` MUST render exactly one `<NuxtPage />` and it MUST be mounted unconditionally. Never put `<NuxtPage />` behind `v-if`, `v-else-if`, or `v-else`, directly or through an ancestor.
- Never replace `<NuxtPage />` with `<RouterView />`. Nuxt pages must use Nuxt's router integration.
- Every Nuxt layout MUST render its default `<slot />` unconditionally. A conditional layout slot makes the `<NuxtPage />` passed through `NuxtLayout` effectively conditional and can trigger `NUXT_E4011` during bootstrap.
- Loading, backend-error, authentication, and authorization screens MUST be rendered as siblings/overlays or with `v-show` while the page slot remains mounted. Do not solve those states by conditionally omitting the page slot.
- Pages that must wait for authentication/authorization MUST react to permission state becoming available (for example with `watch(..., { immediate: true })`) instead of depending on the page being mounted only after login.
- Do not set `pages: false` merely to silence `NUXT_E4011`. That is only allowed if the project intentionally removes Nuxt file-based pages, and requires explicit user approval.
- CI MUST enforce this invariant with the frontend design-rule tests. A change that makes `<NuxtPage />` or a layout's default slot conditional must fail tests.

### Nuxt UI component inventory

This inventory mirrors the official Nuxt UI component catalog (v4.11.0 at the time this rule was added). **Check this list before writing a custom UI primitive.** If the installed Nuxt UI version changes, consult the current catalog and update this inventory.

- **Layout:** `UApp`, `UContainer`, `UError`, `UFooter`, `UHeader`, `UMain`, `USidebar`, `USplitter`, `UTheme`
- **Element:** `UAlert`, `UAvatar`, `UAvatarGroup`, `UBadge`, `UBanner`, `UButton`, `UCalendar`, `UCard`, `UChip`, `UCollapsible`, `UFieldGroup`, `UIcon`, `UKbd`, `UProgress`, `UProgressGroup`, `USeparator`, `USkeleton`
- **Form:** `UCheckbox`, `UCheckboxGroup`, `UColorPicker`, `UFileUpload`, `UForm`, `UFormField`, `UInput`, `UInputDate`, `UInputMenu`, `UInputNumber`, `UInputRating`, `UInputTags`, `UInputTime`, `UListbox`, `UPinInput`, `URadioGroup`, `USelect`, `USelectMenu`, `USlider`, `USwitch`, `UTextarea`
- **Data:** `UAccordion`, `UCarousel`, `UEmpty`, `UMarquee`, `UScrollArea`, `UTable`, `UTimeline`, `UTree`, `UUser`
- **Navigation:** `UBreadcrumb`, `UCommandPalette`, `UFooterColumns`, `ULink`, `UNavigationMenu`, `UPagination`, `UStepper`, `UTabs`
- **Overlay:** `UContextMenu`, `UDrawer`, `UDropdownMenu`, `UModal`, `UPopover`, `USlideover`, `UToast`, `UTooltip`
- **Page:** `UAuthForm`, `UBlogPost`, `UBlogPosts`, `UChangelogVersion`, `UChangelogVersions`, `UPage`, `UPageAnchors`, `UPageAside`, `UPageBody`, `UPageCard`, `UPageColumns`, `UPageCTA`, `UPageFeature`, `UPageGrid`, `UPageHeader`, `UPageHero`, `UPageLinks`, `UPageList`, `UPageLogos`, `UPageSection`, `UPricingPlan`, `UPricingPlans`, `UPricingTable`
- **Dashboard:** `UDashboardGroup`, `UDashboardNavbar`, `UDashboardPanel`, `UDashboardResizeHandle`, `UDashboardSearch`, `UDashboardSearchButton`, `UDashboardSidebar`, `UDashboardSidebarCollapse`, `UDashboardSidebarToggle`, `UDashboardToolbar`
- **AI Chat:** `UChat`, `UChatMessage`, `UChatMessages`, `UChatPalette`, `UChatPrompt`, `UChatPromptSubmit`, `UChatReasoning`, `UChatShimmer`, `UChatTool`
- **Editor:** `UEditor`, `UEditorDragHandle`, `UEditorEmojiMenu`, `UEditorMentionMenu`, `UEditorSuggestionMenu`, `UEditorToolbar`
- **Content:** `UContentNavigation`, `UContentSearch`, `UContentSearchButton`, `UContentSurround`, `UContentToc`
- **Color Mode:** `UColorModeAvatar`, `UColorModeButton`, `UColorModeImage`, `UColorModeSelect`, `UColorModeSwitch`
- **i18n:** `ULocaleSelect`

Some components require optional Nuxt ecosystem integrations or additional dependencies. That is not a reason to recreate the component locally; first check the component documentation and use the official integration when it fits the product requirement.

## Quality gates

Test coverage is a hard repository rule.

- Every change MUST keep automated test coverage at **90.0% or higher** for first-party, testable application code.
- New features and bug fixes MUST include tests in the same change. Do not defer tests to a later issue or PR.
- Coverage must exercise behavior, error paths, validation, authorization, persistence, and lifecycle transitions. Tests that only execute lines without asserting meaningful behavior do not satisfy this rule.
- Do not lower the threshold, exclude packages/files, add coverage ignore directives, or move logic into unmeasured code to make the gate pass.
- Generated code and genuinely non-testable glue may only be excluded when the exclusion is explicit, narrowly scoped, documented in this file, and approved by the user.
- Backend Go coverage is measured with `go test ./... -covermode=atomic -coverprofile=coverage.out` and MUST report total statement coverage of at least 90.0%.
- Frontend coverage is measured with `npm run test:coverage` from `frontend/`. Vitest/V8 MUST report at least 90.0% for **statements, branches, functions, and lines** across `app/**/*.{ts,vue}`.
- Any new frontend logic or view behavior MUST add or extend frontend tests in the same change.
- CI MUST fail when either backend or frontend coverage is below the enforced threshold.
- Before considering implementation complete, run the relevant test suite, coverage gate, formatter/linter/type checks, and build checks.

## Repository layout

- `frontend/` is the Nuxt application root.
- `backend/` is the Go application root.
- `specs/` contains product and architecture specifications.
- Keep frontend and backend dependencies, tests, and build tooling inside their respective application roots.

## Testing conventions

- Prefer deterministic unit tests for pure logic and validation.
- Use temporary directories and temporary SQLite databases for persistence tests.
- Use local fake HTTP/process fixtures for worker/gateway tests; tests must not require a real model, GPU, external network access, or Hugging Face access.
- Test public behavior and important internal invariants, including negative/error cases.
- A regression fix must include a test that fails without the fix.
