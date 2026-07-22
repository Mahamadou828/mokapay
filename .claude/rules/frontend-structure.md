# Frontend structure & conventions

Conventions for React frontends (Vite + React + TypeScript + Tailwind +
React Router). Project-specific design systems (tokens, palettes, fonts) live
in each project's own rules — this file covers structure and style mechanics
that carry across projects.

## Directory layout

```
src/
  pages/<domain>/<page>/     # one folder per page, grouped by domain
    <Page>.tsx               # main page component
    constants.ts             # page-scoped constants (types, lookup tables)
    ui/                      # components unique to this page
    layout/                  # layout components unique to this page
  components/
    ui/                      # shared primitives used across 2+ pages
    layout/                  # shared layout (AppShell, Sidebar, TopBar)
  api/<domain>/index.ts      # API calls, grouped by domain
  hoc/                       # higher-order components (guards, auth wrappers)
  store/<domain>/index.ts    # Redux slices, grouped by domain
  data/                      # static app data (nav structure, domain tokens, mocks)
  types/index.ts             # all TypeScript interfaces
```

A component starts life inside the page that needs it (`pages/<domain>/<page>/ui/`).
It moves to `components/ui/` only once a **second** page needs it.

## Page anatomy

A non-trivial page decomposes like this:

```
pages/home/overview/
  Overview.tsx        ← grid shell, cross-section state, no JSX noise
  constants.ts        ← page-scoped types and lookup tables
  ui/
    Header.tsx        ← pure display, receives only what it needs
    TimelineCard.tsx  ← owns its own editing state
    GoalsPanel.tsx
    StatStrip.tsx
```

Rules:
- The page component owns **cross-section state** (e.g. selected day, mutable
  data shared by several panels) and passes it down via props.
- Sub-components in `ui/` own **purely local UI state** (edit mode toggles,
  hover, open/closed).
- `constants.ts` for anything that would be redefined in multiple sibling files.
- Keep the page component under **~60 lines** — if it grows, something belongs
  in `ui/`.

## Routing

React Router. `BrowserRouter` lives in `main.tsx`. All pages render inside the
shared app shell via `<Outlet />`. Routes are registered in one place
(`src/app/app.tsx`).

## Styling rules

**Inline styles for all dynamic colors.** Computed color values (`oklch`,
`color-mix()`, palette-driven accents) cannot be expressed as Tailwind classes —
use `style={{ color: pal.strong }}` etc.

**CSS classes for static hover states.** Hover colors that never change
(e.g. `rgba(255,255,255,0.05)` on a dark sidebar row) go in a stylesheet as
`.row:hover` rules. This avoids per-element `onMouseEnter/Leave` state for
simple background highlights.

**`onMouseEnter/Leave` only for interactive elements** whose hover color is
dynamic (e.g. a button whose hover tint follows a domain accent). In that case
set `style` properties directly on `e.currentTarget`.

**Tailwind for structural utilities** (flex, grid, padding, overflow) where the
values are static and not color-related.

## State placement

- Local UI state (toggles, hover, open/closed) → `useState` in the component.
- Cross-section state within one page → the page component, passed via props.
- Cross-page / server state → the store (`store/<domain>/`) and `api/<domain>/`.
- Never pass computed style/palette objects across route boundaries as props —
  each page derives its own from the current route.
