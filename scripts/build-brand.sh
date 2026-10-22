#!/usr/bin/env bash
# Renders the brand sources in brand/*.svg into the PNGs in brand/png/.
# Edit the SVG, run this, commit both: never edit a rendered file.
#
# Needs chrome-headless-shell (preferred: it sizes the viewport exactly) or a
# Chromium or Chrome binary: CHROME=/path/to/binary, or one on PATH.
# Text uses DejaVu Sans when installed, so render on a machine that has it for
# output identical to the committed files.
set -euo pipefail

cd "$(dirname "$0")/../brand"

chrome=${CHROME:-}
if [[ -z $chrome ]]; then
	for candidate in chrome-headless-shell chromium chromium-browser google-chrome google-chrome-stable; do
		if command -v "$candidate" >/dev/null; then chrome=$candidate; break; fi
	done
fi
if [[ -z $chrome ]]; then
	echo "build-brand: no Chromium found; set CHROME=/path/to/chrome" >&2
	exit 1
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

# render SOURCE OUTPUT SIZE: SIZE is WIDTHxHEIGHT in CSS pixels.
render() {
	local source=$1 output=$2 width=${3%x*} height=${3#*x}
	# Wrapping the SVG in a page pins it to the viewport, whatever its own size.
	cat >"$tmp/page.html" <<HTML
<!doctype html><meta charset="utf-8">
<style>html,body{margin:0;background:transparent}img{display:block;width:${width}px;height:${height}px}</style>
<img src="file://$PWD/$source">
HTML
	"$chrome" --headless --disable-gpu --no-sandbox --hide-scrollbars \
		--default-background-color=00000000 --force-device-scale-factor=1 \
		--window-size="$width,$height" --screenshot="$tmp/out.png" \
		"file://$tmp/page.html" >/dev/null 2>&1
	mv "$tmp/out.png" "png/$output"
	echo "brand/png/$output"
}

mkdir -p png
render icon.svg icon-512.png 512x512
render icon.svg icon-256.png 256x256
render icon.svg icon-128.png 128x128
render icon-32.svg icon-32.png 32x32
render icon-16.svg icon-16.png 16x16
render readme-banner.svg banner.png 1280x320
render link-preview.svg social-preview.png 1280x640
