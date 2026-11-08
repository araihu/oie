# Image viewer discussion

## Agreed

- Use the supplied WhatsApp viewer screenshots as an interaction reference.
- Previous/next and the thumbnail strip stay within the current conversation.
- Favorite stars the source message, not a separate image-only record.
- The first milestone includes browser/Oie actions: Save original, device Share,
  Copy image, Star, Go to message, zoom and image navigation.
- Unsupported device file sharing opens a fallback menu with Copy image and Save
  original. Forward, Reply and React follow in later protocol-enabled work.
- Reuse Margo's existing image-control mechanics: zoom, Fit, 1:1, pan, pinch/wheel,
  supported fullscreen and keyboard handling, with Fit as the opening mode.
- Any reusable image viewer or control extraction belongs upstream in Goshtoso.
  Margo remains responsible for Markdown parsing/rendering.
- Download/save, sharing and favorites are desired actions. Keep actions within
  browser/device abilities and implemented protocol capabilities.
- Existing implementation fixes continue separately: automatic image downloads
  by default per device, sharper cached previews and retained-original expansion.

## Public component discovery

Margo already implements image and Mermaid viewport mechanics in its embedded
media viewer runtime. Current attachment depends on document-specific markup,
CSS, English labels and an owned expansion dialog. A generic Goshtoso viewport
should reuse those mechanics with localized labels, host-owned expansion,
reactive source replacement/disposal and slots for host actions/navigation.
Oie supplies conversation context and actions through the uniform engine API.

## Open implementation details

- Exact public Goshtoso component/configuration contract and Margo reuse plan.
- Clipboard and sharing capability detection and failure states.
- Source-message favorite API, media pagination and jump-to-message routing.
- Keyboard/touch behavior and precise toolbar/overflow layout.

The feature set is agreed. API 0.6.0 implements source-message stars and a
paginated starred-message collection. Timeline/viewer star actions, message
context menus, cached-original saving and the compact sender header are implemented. See
[message-star invariants](message-stars.md). Zoom/navigation, device sharing,
Copy image, Forward, Reply and React remain unimplemented in this increment.
