#!/bin/sh
# Builds the web form of the MBT manuals into OUT: the two books as sites
# (guide/, reference/), their PDFs and a landing page.  Read the Docs runs it
# with OUT=$READTHEDOCS_OUTPUT/html; locally any directory.
#   sh docs/books/web/build.sh /tmp/site
# Needs typst on the PATH (the version CI pins) and the bookmaster submodule.
set -eu
out=${1:?usage: build.sh OUTDIR}
mkdir -p "$out"
out=$(cd "$out" && pwd)
cd "$(dirname "$0")/.."
web="--features html,bundle --format bundle --input bm-bundle=1 --root . --font-path bookmaster/fonts"
pdf="--root . --font-path bookmaster/fonts --ignore-system-fonts"
typst compile $web ml02-0001.typ "$out/guide"
typst compile $web ml02-0002.typ "$out/reference"
for n in ml02-0001 ml02-0002; do typst compile $pdf $n.typ "$out/$n-0.pdf"; done
cp web/index.html "$out/index.html"
cp -R "$out/guide/fonts" "$out/fonts"
cp "$out/guide/bookmaster.css" "$out/bookmaster.css"
