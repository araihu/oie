# Uniform gallery preview frames

Image and video cards use the same responsive 4:3 preview area in the global
media catalog and conversation media drawer. This balances taller images with
wide videos. Static previews fill the frame; native playback fits the original
video inside it. Loading, cached images and playback retain the same footprint.
Chat message cards keep their existing sizing.

Automatic video preview requests already default on, run near the viewport, and
respect the per-device opt-out. No original video is loaded or played by the
browser until Play. Failed extraction remains an explicit retry. Initial cached
failures now initialize their localized status and retry label, instead of
looking like a preview waiting for a first manual load.

Compact manual/retry controls appear as an accessible icon within the preview,
away from the card's action chevron. Their tooltip and live status explain a
failed preview. They do not add a separate row beneath the media. Source footers
retain their natural content height.

The existing public Goshtoso Button, Panel and icon components are reused.
CSS targets app-owned gallery hooks. The image width override also removes the
340-pixel chat constraint from gallery images.

Validation passes `make check`, the thumbnail browser fixture, native-video
playback fixture and shared-content gallery fixture. Thumbnail checks compare
actual image/video sizes at 390, 1211 and 1440 pixels in light and dark mode,
assert a 4:3 frame, and verify that playback does not resize it. They also cover
automatic requests, cached reuse, explicit opt-out, keyboard manual loading,
failed-preview retry and accessibility. All fixture media is local.

Read-only inspection of the live catalog measured equal image/video frames of
222 × 166.5 pixels at a 1211-pixel viewport and 176 × 132 pixels at 390 pixels.
It produced no mutations, original-video loads or page errors. Evidence is
`/tmp/oie-uniform-media-live.log` and the corresponding screenshots.

Dagger's engine logs confirm that `make check test-race build build-cli` and the
mock build completed without errors. The local client timed out with exit 124,
so this is not a clean CLI completion. Evidence is saved in
`/tmp/oie-uniform-media-dagger-remote-proof.log` and
`/tmp/oie-uniform-media-dagger.log`.
