# Gallery actions and message stars

Shared media, links and document cards have a top-right chevron using the public Goshtoso Dropdown. It appears on hover, keyboard focus and touch devices. The menu preserves canonical navigation to the source message and offers Copy link and Open link. Supported images, videos, PDFs and text documents also offer Download. Link cards retain their existing metadata refresh action in the same menu. Audio and unsupported document formats keep their existing original-link action.

Go to message appears in the card action menu. Card footers show the source
conversation, sender and timestamp without repeating that navigation link.

The menu uses the catalog item's engine, account, conversation and source-message identity. Source navigation keeps the existing HTMX route contract. Copy writes the original URL as plain text. Video and document downloads use the existing same-origin, resource-scoped streaming endpoint with `download=1`; opening a menu never starts playback or a file download.

Image Download reuses the existing preview ensure operation once. A retained original saves directly. A queued original uses bounded status reads through the existing OpenAPI `GET /api/v1/previews/{previewId}` operation, now also proxied by the BFF. This covers older gallery pages without relying on their inclusion in the latest live snapshot. The optional `X-Oie-Engine-ID` header checks the current engine before reading metadata. Status reads do not queue a job or contact a provider. Polling stops on failure, timeout, a changed engine, removed card or route disposal. A matching ready file is saved once through the retained media endpoint.

Each source message now has a clickable star at the bottom-right inside its bubble. Starred messages keep the gold control visible. Unstarred controls appear on hover, focus or touch. The button reuses the existing durable star mutation and reconciliation flow, including unknown/pending states and accessible labels. These app-owned message stars remain distinct from favorite conversations.

Labels and action feedback are localized in English and Brazilian Portuguese. All menu and star positioning uses app-owned hooks and public component slots; no dependency internals or provider client is added to the browser.

Gallery refresh compares a per-item version that excludes independently refreshed preview metadata. Unchanged cards retain their menu, focus and preview nodes. Changed cards defer replacement while their action menu is open or focused; authoritative removals still remove the item. The bounded history cache keeps at most four scopes and 8 × 1024 × 1024 UTF-16 code units of serialized records, allowing the larger menu markup to retain ordinary loaded gallery depths across Back/Forward.

Verification: `make check` and Dagger `make ci-check` pass, including lint, generation drift, native tests, race tests and builds. Host browser fixtures pass for catalog actions, exact copied URLs and downloaded bytes, queued-image status reads, source navigation, link refresh, global/drawer infinite scrolling and history restoration. The star fixture passes direct toggles and 42 bubble-layout cases, with English/Portuguese and touch accessibility checks. Read-only live inspection confirms an open catalog menu remains visible and inside the viewport; real account data was not mutated for validation.
