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
