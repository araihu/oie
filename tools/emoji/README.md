# Emoji data

`generate.py` builds the two local catalogs from Unicode Emoji 17.0 and CLDR 48. Inputs come from the Unicode Consortium, with fixed URLs and SHA-256 checksums in the script. CLDR's `pt` annotations provide the Brazilian Portuguese catalog. The generator requires Python's standard library only and belongs under `tools`, without adding application dependencies.

From the repository root:

```sh
python3 tools/emoji/generate.py
```

The generator downloads its inputs only if the cache is missing and verifies cached inputs too. Use `--cache /path/to/cache` for a different cache and `--output /path/to/output` to compare regenerated catalogs without changing the committed files. Updating the Unicode/CLDR version is deliberate, separate from normal application generation and CI. CI validates the committed catalog identities, localized fields and representative composed sequences without downloading data.

The generated catalogs are modified Unicode data, with 3,944 fully qualified sequences per locale. They retain Unicode order, localized display names and category numbers. The English and Brazilian Portuguese search fields each combine that language's names and keywords with its app-owned `aliases.json` synonyms. A `families` map links skin-tone variants to retained base emoji, including expanded mixed-tone pairs. They omit standalone components. They do not contain emoji artwork or fonts. The system's emoji font controls rendering and support for newer characters.

Copyright © 1991-2025 Unicode, Inc. The data uses Unicode License v3, SPDX `Unicode-3.0`. Preserve `internal/adapters/web/emoji/LICENSE.txt` with the catalogs. The application embeds the license and serves it at `/ui/emoji/LICENSE.txt`; the generated JSON includes its attribution and modification notice.

Sources:

- https://www.unicode.org/Public/17.0.0/emoji/emoji-test.txt
- https://github.com/unicode-org/cldr/tree/release-48/common/annotations
- https://github.com/unicode-org/cldr/tree/release-48/common/annotationsDerived
- https://github.com/unicode-org/cldr/blob/release-48/LICENSE
