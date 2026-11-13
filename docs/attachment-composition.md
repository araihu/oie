# Attachment composition

The composer’s plus menu opens **Image**, **File**, or **Location**. Image
opens a picker filtered to images. File accepts any supported attachment.
Clipboard files and drops on the chat pane open the same attachment preview.
The preview starts with the current composer text as its caption. Cancel or
Escape preserves the original draft. **Add to draft** keeps the reviewed
caption and files. Send and Schedule remain composer actions.

Location lets the user paste a map link or use the device’s current position
when available. It appends an editable link to the draft. A location added
through this menu skips draft link enrichment while the user composes.

The attachment preview is a compact overlay in the existing Oiê chat workspace.
It uses Goshtoso Dialog, Textarea and Button. A row shows each filename and
a local thumbnail in a fixed-size frame for supported images. The browser owns paste/drop
events and unreconciled file selections. Text paste uses the textarea's built-in
behavior.

## State and effects

| State and action | Result | Draft and focus | Outgoing messages |
| --- | --- | --- | --- |
| Available, choose, paste or drop files | Review the selected files before staging | Composer draft stays in place and focus moves to the caption | 0 |
| Review, remove a file | Remove only the selected file | Focus moves to the next removal control or caption | 0 |
| Review, cancel or Escape | Discard the temporary selection | Restore prior focus and leave the draft intact | 0 |
| Unsupported conversation or file over the local limit | Show a localized notice | Leave focus, draft and timeline in place | 0 |
| Confirm, staging still pending | Show progress and guard repeated activation | Keep the preview and allow cancellation | 0 |
| Stage fails or response is lost, retry | Reuse the file's staging ID | Keep caption and selected files available for retry | 0 |
| Add to draft after successful staging | Apply caption and files to the scoped draft | Focus the composer | 0 |
| Review a location and add it to the draft | Append the map link without requesting a draft link preview | Keep the existing text and files, and focus the composer | 0 |
| Geolocation denied, unavailable or cancelled | Offer the link field | Leave the existing draft unchanged | 0 |
| Send from the composer | Use the ordinary message operation and receipt | Preserve the existing send recovery behavior | 1 logical send |
| Navigation or cancellation while staging | Discard the preview; a late response cannot apply it | Keep the original draft in its conversation | 0 |

Staging uses the existing conversation-scoped multipart endpoint. Staging IDs
stay stable across retries in the preview. The engine rechecks file availability
and provider limits at acceptance. A file whose staging completed before a
cancellation can remain in the engine's staging store. Unreferenced-file
cleanup is tracked in O09 of the specification.

The app accepts up to 16 attachments, with an engine limit of 20 MiB per
file. A provider can impose a lower limit. The contact or room receives
the caption followed by the provider's download links.

A pending message shows its filenames and supported thumbnails in the timeline.
The same attachments appear while waiting for confirmation or retrying after
a reload.

The XMPP adapter uploads attachments before writing the outgoing chat
message. Once the message is accepted, the existing outbox tracks the send
and governs recovery after a restart or a lost response.

Browser checks cover Chromium at 390 px and 1440 px in Brazilian Portuguese
and English. Native operating-system drag and drop requires a Wails integration.

## Verification

The focused browser test exercises ordinary text paste, file paste and drop,
preview cancellation, preservation of draft text and selection, delayed staging,
the local file-size limit, a lost staging response retried with the original
ID, and draft restoration after reload. It samples composer and timeline
geometry during cancellation and checks the preview with axe. Explicit send
was verified through a development Prosody service. The receiver retained the
caption and the provider's file link. The XMPP integration test also verified
that a scheduled attachment downloads with its original bytes.
