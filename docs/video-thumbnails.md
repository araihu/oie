# Video previews and playback

The [accepted media requirements](oie-spec.md#8-media-and-downloaded-files)
cover draft attachments and received video cards in direct chats and rooms.
The same received-video cards appear in the Media catalog and conversation
details. Video plays only after an explicit action.

## Draft attachments

The composer decodes the chosen browser File directly using an off-screen
video element. A canvas captures a small JPEG near one second. Extraction and
staging proceed independently. A fixed thumbnail slot holds the play control
even if a still frame is unavailable. Native playback opens in a dialog.
After a reload, or after selection through the Flatpak file chooser, the
browser can regenerate the thumbnail from the conversation's staged file
within the existing 20 MiB per-file limit. Removing the attachment, changing
drafts or leaving the view releases the preview resources.

## Received video

The card first uses a cached image, including posters created by older releases.
The existing engine thumbnail API can retrieve a small image supplied with a
video in XMPP stateless file sharing. It checks the current message reference
and stores the normalized image. The engine uses its existing outbound HTTP
policy and a 1 MiB image limit.

When the engine has no usable image, the browser requests a sample through
the same-origin media route. The BFF requests the first 5 MiB at most and
rejects an oversized response before forwarding its body. It reuses the
canonical media source, MIME checks and outbound network policy used for
playback. A partial video can lack the needed metadata or frames. A different
codec or container may also be unsupported by the browser or WebView. In
both cases the card still offers Play and explicit thumbnail retry. Oiê
does not invoke FFmpeg.

The existing device setting controls automatic preview requests near the
viewport. The browser runs at most two received preview jobs at a time. JPEGs
are scoped to the engine, account, conversation, message revision and item.
They use the shared bounded cache with device storage where available. Jobs
stop when their cards leave the page. Original video playback keeps its
existing same-origin streaming and range support.

XMPP references: [XEP-0447](https://xmpp.org/extensions/xep-0447.html),
[XEP-0446](https://xmpp.org/extensions/xep-0446.html),
[XEP-0264](https://xmpp.org/extensions/xep-0264.html).
