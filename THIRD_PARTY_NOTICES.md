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

## Generated command-line client

The runtime and templates copied into `oiectl` by `araihu/oapi-cli-gen`
are available under MIT. The standalone CLI module carries the license in
[`cli/internal/cliruntime/LICENSE`](cli/internal/cliruntime/LICENSE).
