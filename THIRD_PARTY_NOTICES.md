# Bundled browser assets

Oie is licensed under Apache License 2.0. Bundled third-party files retain their
own notices and licenses.

The document viewer uses the display API and worker from Mozilla PDF.js
6.5.438, distributed as `pdfjs-dist` under Apache License 2.0. The pinned
package source, integrity and individual file checksums are recorded in
`internal/adapters/web/pdfjs/manifest.json`. Its license is included beside the
assets and is available at `/ui/pdfjs/LICENSE`.

The distribution also includes Adobe character maps, PDFium/Foxit font data,
image decoders and an ICC profile. Their notices are included in the respective
`cmaps`, `standard_fonts`, `wasm` and `iccs` directories. They use BSD, MIT and
CC0 terms. Liberation fonts and the PDF JavaScript scripting sandbox are not
included. Missing standard sans-serif font data uses the browser's system fonts.

Run `python3 tools/vendor-pdfjs.py` to verify the assets without network access.
Use `python3 tools/vendor-pdfjs.py --update` to restore the exact pinned package.
The tool verifies its SHA-512 package integrity before writing selected assets.

## lru-cache browser runtime

`lru-cache` 11.2.6 is distributed under the Blue Oak Model License 1.0.0.
The browser ESM build is served locally. The license and pinned archive/source
checksums are in `internal/adapters/web/cache_vendor`. Reproduce the asset with
`python3 tools/vendor-lru-cache.py`. Project: https://github.com/isaacs/node-lru-cache.

## Unicode emoji data

The localized picker catalogs and protocol reaction validation bases derive from
Unicode 17.0 and CLDR 48 data under Unicode-3.0. Copyright © 1991-2025 Unicode,
Inc. The license is included at `internal/adapters/web/emoji/LICENSE.txt` and
served at `/ui/emoji/LICENSE.txt`. Regenerate the validation base table with
`python3 tools/generate-reaction-emoji-bases.py`.
