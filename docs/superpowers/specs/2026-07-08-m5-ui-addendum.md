# M5 UI Addendum — Picked Winner & View-Contract Updates

Status: winner approved by user 2026-07-08; gates M6
Companion: `prototypes/2026-07-08-m5-winner.html` — the winning throwaway
mock (self-contained HTML, mock data; reference only, not production code)

## Process & outcome

Two contrasting prototypes were built within the user-chosen direction
(clean utilitarian tracker, desktop-first, phone-usable): variant A
"Shelf" (card grid, cover-forward) and variant B "Ledger" (dense
sortable table). The user picked **B's shell with A's detail page**,
plus refinements iterated live: media-type row tinting, solid state
chips, and a row-density toggle.

## Design tokens

Neutrals are cool slate with a blue-green bias; one accent; semantic
state colors distinct from the accent; per-media-type hues distinct
from both. Values below are the prototype's and are the starting point
for M6, not pixel law.

| Token | Light | Dark |
|---|---|---|
| bg / surface | `#F7F8F8` / `#FFFFFF` | `#131718` / `#1C2122` |
| ink / muted | `#1A1D1E` / `#5C6668` | `#ECEFF0` / `#9AA5A6` |
| accent (petrol) | `#0F766E` (tint `#E6F2F0`) | `#2DA99E` (tint `#12312E`) |
| state: in_progress | `#B45309` | `#E8A04C` |
| state: done | `#15803D` | `#5DBB7D` |
| state: abandoned | `#B91C1C` | `#E07070` |
| type: movies/TV (steel blue) | `#3663A8` (tint `#E8EEF8`) | `#7EA6DD` (tint `#1C2938`) |
| type: books (ochre) | `#8A6116` (tint `#F6EFDD`) | `#D3A84F` (tint `#332A14`) |
| type: games (forest green) | `#2F6B33` (tint `#E7F1E5`) | `#7FBF7F` (tint `#1C2B1A`) |

- **State chips are solid-filled** (state color background; white text
  in light theme, near-black in dark) so they stay legible on
  type-tinted rows. Chosen explicitly after tint-on-tint failed.
- **Type identity appears as**: nav-tab dots, tinted type chips, and —
  the load-bearing device — **row background tints** on the landing
  page panels and the tab-view table.
- Known accepted tension: games-green sits near done-green and the
  petrol accent; the solid chips resolve it in practice.
- Typography: system UI stack for interface, system mono stack
  (`tabular-nums`) for counts/years/ratings/dates. Deliberate: the app
  ships `embed.FS` with no CDN, so system faces are production reality.
  13px base, dense.
- Both themes ship from day one, driven by `prefers-color-scheme`;
  token-level palette so components never hardcode colors.

## Layout decisions (per view)

**Top bar** — wordmark; Home + three group tabs, each with a type dot
and a count; quick-search input right-aligned (the add flow's entry
point, per the original contract).

**Landing** — two columns on desktop: *Continue* (in_progress items)
and *Newly available on your services*. Rows inside both panels are
**grouped by media type** with subheaders (dot + group name + count)
and type-tinted backgrounds. Below, a *Library* panel: one line per
group with want-to / in-progress / done counts, linking into the tab.

**Tab view** — a dense sortable table, not cards. Columns: thumbnail,
title (+genre sub-line), type chip, year, rating, availability badges,
state chip, added date. Clicking a column header sorts and toggles
direction. State filters render as flat text links with counts
(accent underline = active), Movies/TV gets an All/Movies/TV pill
sub-discriminator, plus the available-to-me checkbox. A result-meta
line under the table states count and sort. Rows are type-tinted.

**Row density** — an S/M/L row-height toggle (38/56/84px thumbnails)
lives in the table toolbar. The user's read on mock covers: **M
spacing feels right, but expect L with real art → default Large,
re-validate with real covers early in M6.** The toggle ships as a
persisted preference (settings), not a throwaway.

**Detail page** — variant A's layout inside the ledger shell: a large
(~250px) sticky cover left; right column stacked as Status (legal
transitions only, first option rendered primary) → Verdict (terminal
states only) → Ratings (one card per source: display string, source
name, link-out) → Availability (chips: subscribed = accent tint, owned
= filled + store-page link, other = outline) → Notes (textarea,
Edit/Preview toggle, explicit Save, Markdown) → Details (provider,
provider id, added, refreshed).

**Add flow** — quick-search dropdown showing candidates (thumbnail,
title, year, disambiguation); duplicate add navigates to the existing
item, per the original contract.

## View-contract changes vs. spec Section 4

The original contract survives intact except:

1. **Sort direction is now part of the filter state.** Column-header
   sorting toggles asc/desc, so the URL scheme gains a direction param
   (e.g. `?sort=year&dir=desc`). Defaults: added desc, year desc,
   rating desc, title asc.
2. **Landing sections are grouped by media type** (contract previously
   implied flat lists).
3. **New persisted preference: row density** (S/M/L), stored in
   `settings`, applied to the tab-view table.
4. **Card-vs-table is settled: table.** No card view in v1.

Everything else (route surface, filters, legal-transitions-only,
verdict/notes behavior, subscribed-highlight semantics) is unchanged.

## HTMX interaction mapping

Every prototype control maps to a partial swap: state filter / sort /
sub-discriminator / available-to-me / density re-render the table
partial (URL-encoded state, bookmarkable); tab clicks swap the main
view; detail transitions re-render the detail partial; notes preview
is a partial swap of the notes block; quick-search renders a candidate
dropdown partial (debounced). No full-page reloads anywhere except
initial load.

## Explicitly out of scope for this addendum

- Real cover art (M6; covers already download to `covers/{id}.jpg`) —
  the density default above is provisional until seen with real art.
- Settings page layout (contract in spec Section 4 stands; not
  prototyped — low risk, it's a checklist + status page).
- Phone layout fine-tuning (prototype collapses to one column and was
  deemed acceptable; polish in M6).
