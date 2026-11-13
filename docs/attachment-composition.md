# Attachment composition

In a conversation, the attachment button, a file pasted into the composer and
a file dropped onto the chat pane open the same preview. The preview starts
with the current composer text as its caption. Cancel or Escape returns to
the original composer without changing the draft. Add to draft keeps the
reviewed caption and files for the normal composer actions, including
scheduling. Send submits them together through the existing outbox.

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
| Send after successful staging | Use the ordinary message operation and receipt | Preserve the existing send recovery behavior | 1 logical send |
| Navigation or cancellation while staging | Discard the preview; a late response cannot apply it | Keep the original draft in its conversation | 0 |

Staging uses the existing conversation-scoped multipart endpoint. Staging IDs
stay stable across retries in the preview. The engine rechecks file availability
and provider limits at acceptance. A file whose staging completed before a
cancellation can remain in the engine's staging store. Unreferenced-file
cleanup is tracked in O09 of the specification.

The app accepts up to 16 attachments, with an engine limit of 20 MiB per
file. A provider can impose a lower limit. The contact or room receives
the caption followed by the provider's download links.

The XMPP adapter uploads attachments before writing the outgoing chat
message. Once the message is accepted, the existing outbox tracks the send
and governs recovery after a restart or a lost response.
