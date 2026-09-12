# Anvil Design System

The single source of design truth. Every page and component follows this. When in
doubt, match this document, not another page.

## Philosophy

Stoic, engineered, restrained — a security tool, not a gamer product. Darkness is the
native medium. **One accent (amber); everything else is grayscale.** Color is earned:
it appears only on genuine state (a live event, a down service, a first blood). If two
things on a screen are amber, one of them is wrong.

Separate regions with **hairline borders and near-black elevation steps, never glows or
heavy shadows.** De-emphasize with a lighter gray step, never with colored text.

## Color tokens

Neutrals — Tailwind `stone` (warm). Roles:

| Role | Token | Use |
|------|-------|-----|
| Base background | `bg-black` / `stone-950` | page ground |
| Surface | `stone-900/40` | cards, panels (translucent, not a solid block) |
| Elevated / hover | `stone-800/40` | row hover, raised surface |
| Hairline border | `stone-800` | all dividers/borders (1px) |
| Text primary | `stone-100` | headings, key values |
| Text secondary | `stone-300` | body, names |
| Text tertiary | `stone-400/500` | labels, metadata |
| Text quaternary | `stone-600/700` | timestamps, faint captions |

Accent — amber, used sparingly:

| Token | Value | Use |
|-------|-------|-----|
| `text-amber-500` / `amber-500/90` | brand amber | the one action or the one number that matters in a view |
| `amber-500/60–70` | dim amber | hover, held/active accent, the live dot |

Semantic (muted; reserved for state only):

| Role | Value |
|------|-------|
| up / success | `#4b7355` |
| down / error | `#b0453a` (allowed to pop) |
| warn / faulty | `#9c7a30` |
| info / recovering | `#3f6a86` |
| flag-missing | `#9c5a30` |
| first blood | `#e0483c` (the one sanctioned saturated pop) |

**Sanctioned categorical color** — a few signals *earn* real color because color IS the
meaning; use the shared helpers, never ad-hoc hues:
- **Difficulty** (`difficultyClass` in `rank.ts`): easy=green, medium=yellow, hard=red,
  insane=purple — outlined pills (colored text + border + dark tint).
- **Resource type** (`resourceClass`): VM=purple, container=blue.
- The landing hero highlight word is cyan.
These are the only places saturated color appears in chrome; everywhere else stays muted.

Team / series palette — **muted, low-chroma** (see `$lib/rank.ts`). Never fully
saturated. Per-team identity is a small dot or a thin accent bar, never a saturated
card fill. Charts default to amber-on-stone for a single series; the muted team palette
is used only when distinguishing many series, capped at ~10 lines.

## Typography

- **JetBrains Mono** for all data, numbers, ranks, timers, IDs, labels, headings — the
  engineered identity. A clean sans is acceptable for long prose (challenge writeups).
- Compact count summaries pair a monospaced value with a small uppercase Inter label. This
  is the deliberate exception for human-readable microcopy; keep it restrained and tracked.
- Icon-adjacent compact text uses the shared `optical-label` half-pixel baseline correction.
  Do not tune individual SVGs unless their artwork is demonstrably off-center in its view box.
- Text inside 10–11px pills uses `badge-label` instead: its full-pixel correction centers the
  smaller mono glyphs without moving or resizing the surrounding border.
- `tabular-nums` on **every** numeric value (scores, ranks, times) so digits don't jitter
  on live update and columns align. Non-negotiable on the scoreboard.
- Weight band **400 / 510 (medium) / 590 (semibold)**. Never 700+ — heavy reads as gamer.
  Body 400, UI labels/medium 510, headings 590.
- Section headers: Title Case (never UPPERCASE), `font-semibold text-stone-200`, ~15px. Tiny
  table column headers may stay uppercase, but panel/section titles read in normal case.
- Tight negative tracking on large display numbers.

## Spacing, radius, borders

- 8px spacing grid. Table rows ~36–40px tall, 12–16px horizontal cell padding.
- Radius scale small: `rounded-md` (6px) default, `rounded-lg` (8px) for cards, `rounded-full`
  for pills/status chips. Avoid large radii — they read playful.
- Borders are 1px hairlines in `stone-800`. Row separators `border-stone-800/60`.

## Components (shared — build once, reuse)

`$lib/components/` — the vocabulary every page composes from:

- **Card** — surface panel: `bg-stone-900/40 border border-stone-800 rounded-lg`, header row
  `px-4 py-3 border-b border-stone-800` with an uppercase section title.
- **PageHeader** — page title (`text-2xl font-bold text-stone-100 tracking-tight`) + subtitle.
- **StatTile** — a tertiary-gray caption above a large mono value. The profile/stat unit.
- **Badge / Pill** — status + category chips (`rounded-full`/`rounded`, muted bg + text).
- **DataTable** patterns — hairline separators, right-aligned tabular-nums numbers, sticky
  header, restrained row hover (`hover:bg-stone-800/20`), no zebra.
- **Sparkline** — sharp, thin, muted; amber only for the viewer's own row.
- **LineChart** — step curve for cumulative data, muted series, x-axis labels, leader emphasis.
- **EmptyState** — one terse mono line in tertiary gray. No illustrations.
- **Skeleton** — card-surface rows with subtle shimmer; skeleton the real grid, avoid spinners
  on data-dense views.
- **RankCard** — the shareable standing card (Canvas → PNG download).

## Charts

- Cumulative scores are **step functions** (flat between solves, jump at a solve) — use
  step interpolation, never smoothing that invents motion. Accuracy over prettiness.
- Always label axes (time on x, value on y). Cap multi-series charts at ~10 lines.
- Muted palette; emphasize one series (leader) and dim the rest.

## Responsive

Every page must work from ~360px (mobile) to wide desktop. Wide tables/grids scroll
inside `overflow-x-auto`; the page body never scrolls sideways. Stack columns on small
screens (`grid-cols-1` → `sm:`/`lg:` up), hide non-essential table columns on narrow
widths (`hidden md:table-cell`), and keep tap targets comfortable. Test at 375px and 1440px.

## Interaction

- Hover is a whisper of white (`stone-800/20–40`), never a colored highlight (amber hover
  only on genuinely interactive accent elements).
- Live updates: new rows fade in; the affected row briefly pulses (amber, or blood-red for a
  first blood) and decays in ~600ms. Rank reordering animates (FLIP), never teleports.
- Empty/loading states are terse and mono. Focus rings are visible (keyboard-heavy audience).

## Anti-patterns (do not)

- No saturated/rainbow fills, no neon, no glows, no gradients-as-decoration (the legacy
  green/cyan text gradients are removed).
- No color used purely decoratively. No bold 700+ weights. No large radii.
- No per-page one-off color choices — take colors from the tokens above.
