# Oiê identity and browser assets

The display name is **Oiê**, a warm, informal greeting in Portuguese. Use that
spelling in the logo, interface and product prose. Repository names, executable
names, URLs and other ASCII identifiers remain `oie`.

The approved identity is the flat Noto Emoji speech balloon with the moon face,
without gradients, shading or cheek highlights. The shared `araihu/assets`
pipeline owns source geometry, variants, favicon and platform rasterization.
The web adapter embeds a selected, checksum-recorded distribution snapshot under
`internal/adapters/web/brand`. `SOURCE.json` records the exact asset revision and
whether the source was an unpublished release candidate. Third-party notices
and license texts accompany the copied assets and executable.

## BFF resource contract

- `GET`/`HEAD /ui/brand/{file}` serves embedded SVG, PNG, JSON and notice files,
  without contacting the engine. Paths outside this embedded directory fail.
  Responses have content types, a content-derived ETag, and a one-hour public
  cache lifetime. Conditional requests can return 304.
- `GET`/`HEAD /manifest.webmanifest?lang=en|pt-BR` returns a localized web app
  manifest with the Oiê name, standalone display, fixed `/` identity/start/scope,
  and generated 192/512 ordinary and maskable icons. It is not cached across
  locale preferences.
- The document links SVG/PNG favicons, Apple touch icon and the manifest.
  Navigation composes light/dark shared logos through the public Goshtoso
  Sidebar logo slot, respecting the app theme override as well as system mode.

This increment supplies browser identity and install metadata. It does not add
service-worker caching, offline messaging or web push.

## Validation on 2026-11-08

`make check` passed, including contract lint, golangci-lint, generated-file
checks, Go/CLI tests and vet, formatting and Compose validation. The browser
identity test passed all 16 combinations of 390/1440 px, Goshtoso/Minimal,
light/dark, and English/pt-BR with no page errors, overflow or axe violations.
It also verifies manual color mode against the opposite system preference.

Local Dagger passed checks, race tests, builds, real XMPP integration and every
browser suite before the final `engine-browser.cjs` test. Its brand-test
snapshot covered eight layouts; the expanded 16-layout test then passed locally.
The final real-engine browser workflow timed out in `pickAccount` while waiting
for an account-scoped draft before any conversation was open. That unchanged
baseline helper requires `body.dataset.accountId` to identify an active
conversation account, while the empty-chat view deliberately provides none.
The helper and runtime code are unchanged from base commit c358845. This run
therefore does not count as a passing complete Dagger pipeline. Artifact export
also failed with `waiting for client session attachables: context deadline
exceeded`; the remaining client was interrupted after that failure.

The shared snapshot comes from araihu/assets commit
342a1c87a1f3784521cd6efc58820a1bd44b84b7, proposed in PR 42 as candidate v0.2.7.
No Oiê pull request or public assets release was created. This app increment is
isolated from the main session's ongoing engine edits and can be integrated by
its commit.
