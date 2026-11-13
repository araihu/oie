# Third-party notices

Oiê is licensed under the [GNU General Public License, version 3](LICENSE)
(`GPL-3.0-only`). Bundled third-party files retain their own notices and licenses.

## Wails native runtime

The desktop command uses Wails v3.1.0 and its embedded browser runtime under the
MIT License. Source: https://github.com/wailsapp/wails/tree/v3.1.0.

Copyright (c) 2018-Present Lea Anthony

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.

## PDF.js browser assets

The document viewer uses the display API and worker from Mozilla PDF.js
6.5.438, distributed as `pdfjs-dist` under Apache License 2.0. The pinned
package source, integrity and individual file checksums are recorded in
`internal/adapters/web/pdfjs/manifest.json`. Its license is included beside the
assets and is available at `/ui/pdfjs/LICENSE`.

The PDF.js worker includes the MIT-licensed Brotli JavaScript decoder.
The release packages and OCI image include the decoder's MIT notice.

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

## Interface icons and browser runtime

Goshtoso embeds the Heroicons 2.2.0 sprite under MIT, HTMX 4.0.0 under 0BSD,
and Alpine.js 3.17.2 and its plugins under MIT. Its embedded files include
the Heroicons and runtime license texts. The release packages and OCI image
include the notices for Alpine's bundled Vue code, focus-trap and tabbable.
The focus-trap 8.0.0 build includes Babel 7.28.6 helpers and Regenerator.
Their MIT notice is in `javascript/@babel/helpers/LICENSE` under the packaged
license directory.
Oiê's additional Heroicons sprite uses the same MIT license. The source and
license text are recorded in `internal/adapters/web/appicons`.

The desktop archives include the notice for Nano ID in the Wails runtime.
The release packages and OCI image carry notices for the JavaScript bundled
by Margo, including Mermaid 11.16.1, KaTeX and the
libraries identified in the Mermaid source maps. DOMPurify 3.4.0 is distributed
under its Apache-2.0 option.

The Oiê logo and platform icons adapt Noto Emoji SVG artwork under Apache-2.0.
Their provenance and modifications are recorded in
`internal/adapters/web/brand/NOTICE.txt` and `SOURCE.json`.
Arai Hû also grants Apache-2.0 rights for its Oiê and Goshtoso artwork,
including the Goshtoso v0.6.2 icons from Assets v0.2.6. See
`internal/adapters/web/brand/NOTICE-ARAIHU.txt` for the upstream grant and
trademark terms. The Noto attribution and upstream license are retained.

## OCI image certificates

The server image ships the CA bundle from Debian's `ca-certificates`
20250419~deb12u2, built from Mozilla certificate data under MPL-2.0. The
image includes the Debian copyright file at
`/licenses/ca-certificates/copyright`. The exact Debian source is in
`native-sources/` of the Linux corresponding-source archive.

## Generated command-line client

The runtime and templates copied into `oiectl` by `araihu/oapi-cli-gen`
are available under MIT. The desktop archives carry its license at
`licenses/oapi-cli-gen/LICENSE`. The standalone CLI module carries the same text in
[`cli/internal/cliruntime/LICENSE`](cli/internal/cliruntime/LICENSE).
