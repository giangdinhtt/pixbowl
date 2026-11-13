# pixbowl logo

The mark is a capital **P** whose counter is a **square pixel** — the initial and
the idea in one shape. In the primary logo that P **is** the first letter of the
name, set tight against `ixbowl`: the name reads as one word, not as a symbol
parked next to a wordmark. The remaining letters are Inter SemiBold, outlined to
paths and tracked at −1.8%; the P's 32/160 stroke matches Inter's stem weight.

## Files

| File | Use |
|---|---|
| `pixbowl-logo.svg` | **primary logo**: the P mark doubles as the first letter of the name |
| `pixbowl-logo-white.svg` | one-colour reversed logo |
| `pixbowl-logo-accent.svg` | two-colour logo for dark backgrounds (accent P, white text) |
| `pixbowl-symbol.svg` | standalone mark, ink on light backgrounds |
| `pixbowl-symbol-white.svg` | reversed mark for dark backgrounds |
| `pixbowl-wordmark.svg` | wordmark only (headers, footers, README badges) |
| `pixbowl-horizontal.svg` | mark + wordmark side by side (baseline aligned) |
| `pixbowl-stacked.svg` | mark above the wordmark (avatars, covers, narrow columns) |
| `pixbowl-symbol-small.svg` | simplified cut for 16–24 px (strokes 40, counter 56) |
| `pixbowl-favicon.svg` | favicon master: dark tile + white small-size P |
| `icons/favicon.ico`, `icons/favicon.svg` | browser tab |
| `presentation.html` | concept board with README, terminal, website, app-icon and social mockups |
| `icons/apple-touch-icon.png`, `icons/icon-192.png`, `icons/icon-512.png` | app / PWA |
| `icons/site.webmanifest`, `icons/head-snippet.html` | drop-in web icons |

The symbols are single-path SVGs with a real hole (`fill-rule="evenodd"`), so
they scale, cut and invert without artefacts. Below 24 px the standard P's
counter drops under 2 px and greys out, so the favicon set uses the small-size
cut on a dark tile: the white P keeps its contrast and the counter stays a
readable dark square.

## Colour

| Name | Hex | Notes |
|---|---|---|
| Ink | `#0E1014` | primary on light backgrounds |
| Paper | `#FFFFFF` | reversed mark |
| Accent | `#7C9CFF` | the P in `pixbowl-logo-accent.svg`; dark backgrounds only (2.6 : 1 on white) |

Two colours are enough. The one-colour versions must still work; never rely on
the accent to carry the shape.

## Clear space and sizes

- **Clear space:** at least the width of the counter (≈16% of the symbol height)
  on every side.
- **Minimum sizes:** symbol 24 px (16 px with `pixbowl-symbol-small.svg` or the
  favicon master); horizontal lockup 160 px wide; stacked lockup 96 px wide.
  Below that use the symbol alone.
- Do not redraw or re-typeset the wordmark; the outlines are the master.

## Don't

- Fill the counter — it is the pixel, and the reason the mark works.
- Rotate, outline, add shadows, bevels or gradients.
- Stretch or condense; scale uniformly.
- Place the ink mark on a dark surface (use the white version).
- Put the accent on white or on a busy photo.
- Recolour the mark outside the palette above.

## Typography

The wordmark is outlined from **Inter SemiBold** (SIL Open Font License), so no
font file is required to use the logo. If you typeset the name elsewhere, use
Inter and keep the same tracking.
