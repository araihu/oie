# Gallery media spacing

Gallery tiles use their natural height. The source footer follows the preview
instead of moving to the bottom of the tallest tile in the row. A ready image
hides its empty fallback-action region; missing/failed images retain their load
and recovery actions. Gallery videos compose the public Goshtoso Panel body
slot, avoiding Card's unused title/content region. Chat video cards keep their
existing composition.

Validation on the isolated change passed `make check` and the shared-content
and native-video browser fixtures. Read-only rendering against retained data
measured zero space between media and source footer at 1211 and 390 pixels,
in light and dark mode, without mutations, external requests or page errors.
Evidence: `/tmp/oie-gallery-spacing-proof.log` and
`/tmp/oie-gallery-spacing.png`.

Dagger `make ci-check` was attempted but timed out connecting to its engine
before CI execution. This is not recorded as a CI pass. The log is
`/tmp/oie-gallery-spacing-dagger.log`. The first host check hit temporary-file
allocation errors; the passing retry used a dedicated tmpfs build directory.
