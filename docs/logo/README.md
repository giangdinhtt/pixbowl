# pixbowl logo

The mark is a capital **P** whose counter is a **square pixel** — the initial and
the idea in one shape. The wordmark is Inter SemiBold, outlined to paths and
tracked at −1.8%.

## Files

| File | Use |
|---|---|
| `pixbowl-symbol.svg` | primary mark, ink on light backgrounds |
| `pixbowl-symbol-white.svg` | reversed mark for dark backgrounds |
| `pixbowl-wordmark.svg` | wordmark only (headers, footers, README badges) |
| `pixbowl-horizontal.svg` | mark + wordmark side by side (baseline aligned) |
| `pixbowl-stacked.svg` | mark above the wordmark (avatars, covers, narrow columns) |
| `icons/favicon.ico`, `icons/favicon.svg` | browser tab |
| `icons/apple-touch-icon.png`, `icons/icon-192.png`, `icons/icon-512.png` | app / PWA |
| `icons/site.webmanifest`, `icons/head-snippet.html` | drop-in web icons |

The symbols are single-path SVGs with a real hole (`fill-rule="evenodd"`), so
they scale, cut and invert without artefacts.

## Colour

| Name | Hex | Notes |
|---|---|---|
| Ink | `#0E1014` | primary on light backgrounds |
| Paper | `#FFFFFF` | reversed mark |
| Accent | `#7C9CFF` | dark backgrounds only — on white it is 2.6 : 1 |

Two colours are enough. The one-colour versions must still work; never rely on
the accent to carry the shape.

## Clear space and sizes

- **Clear space:** at least the width of the counter (≈16% of the symbol height)
  on every side.
- **Minimum sizes:** symbol 16 px; horizontal lockup 160 px wide; stacked lockup
  120 px wide. Below that use the symbol alone.
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
