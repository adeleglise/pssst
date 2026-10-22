# Brand

The SVG files here are the sources. `scripts/build-brand.sh` renders them into
`png/`; edit a source, run the script, commit both. Never edit a PNG by hand.

```sh
CHROME=/path/to/chrome-headless-shell scripts/build-brand.sh
```

| Source | Renders to | Use |
| --- | --- | --- |
| `icon.svg` | `png/icon-512.png`, `icon-256.png`, `icon-128.png` | 48 px and up: Helm chart, grafana.com listing, avatars. |
| `icon-32.svg` | `png/icon-32.png` | 20 to 40 px: simplified, thicker strokes. |
| `icon-16.svg` | `png/icon-16.png` | Below 20 px: the pulse alone. |
| `mark.svg` | | Next to the name, on a light background. |
| `mark-mono.svg` | | One colour through `currentColor`. |
| `readme-banner.svg` | `png/banner.png` | 1280 × 320, the README header. |
| `link-preview.svg` | `png/social-preview.png` | 1280 × 640, GitHub's social preview and any link card. |

## What it means

The speech bubble is the **declared** status: what a provider says about
itself. The pulse inside it is the **observed** status: what the probes
measure. The pulse drops while the bubble stays calm, which is the one case
PSSST exists to catch: a failure the status page does not mention. The two
shapes never merge into one, just as the exporter never merges the two signals.

## Colours

| Name | Hex | Role |
| --- | --- | --- |
| Night | `#0E1A2B` | Background, the bubble on light backgrounds. |
| Deep night | `#0B1522` | Icon tile on the banner and preview. |
| Mist | `#E6EDF5` | The bubble, body text on dark. |
| Slate | `#8FA3BA` | Secondary text. |
| Signal amber | `#F5A524` | The pulse, the one highlighted phrase. Use it for the unannounced failure and nothing else. |
| Operational green | `#4ADE80` | Only in the preview, for "operational". |
| Failing red | `#F87171` | Only in the preview, for "failing". |

## Type

DejaVu Sans (Bitstream Vera licence), falling back to Helvetica and Arial. The
sources reference the font by name rather than embedding it, so render on a
machine that has DejaVu Sans installed to reproduce the committed PNGs.

## Using the logo

- Keep clear space around the icon of at least a quarter of its width.
- Do not recolour, stretch, rotate or add effects. `mark-mono.svg` is the
  single-colour variant.
- Do not use it to suggest that a payment provider endorses PSSST or that PSSST
  speaks for one. The provider names in the inventory belong to their owners.
