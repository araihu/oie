# Attachment composition

Use the plus menu to pick an image or file. Pasting a file into the composer or
dropping it on the chat pane also adds it directly to the draft. The message box
holds the caption. Each attachment can be removed individually, and supported
images have a thumbnail in the draft. Previews wrap onto new rows when space
is limited. Use the composer to send or schedule
the message.

## State and effects

| State and action | Result | Draft and focus | Outgoing messages |
| --- | --- | --- | --- |
| Choose, paste or drop files | Show the selected files in the composer and start staging | Make the existing caption available for editing | 0 |
| Remove a file while staging | Cancel that file’s request and remove its row | Keep the typed caption and other files | 0 |
| Unsupported conversation or file over the local limit | Show a localized notice | Preserve the existing draft | 0 |
| Stage fails or its response is lost | Offer Retry and Remove in that file’s row | Keep the caption and file selection | 0 |
| Retry staging | Reuse the file’s staging ID | Keep the caption editable | 0 |
| Staging succeeds | Retain the file in the scoped draft | Preserve text typed during the request | 0 |
| Send from the composer | Use the ordinary outbox and receipt | Preserve the normal send recovery behavior | 1 logical send |
| Navigation or scheduled-draft switch during staging | Cancel the outstanding selections | A late response cannot apply to another draft | 0 |

Staging uses the existing conversation-scoped multipart endpoint. Send and
Schedule become available after all selected files finish staging or the user
removes the remaining selections. Staged attachments survive a reload.
Unfinished file selections are held in the current view. A file whose request
committed before cancellation can remain in storage. Cleanup is tracked in O09.

The app accepts up to 16 attachments, with an engine limit of 20 MiB per file.
The provider can impose a lower limit. The XMPP adapter uploads accepted
attachments before writing the outgoing chat message. The recipient receives
the caption followed by the provider’s download links. A pending message shows
filenames and supported thumbnails while the engine confirms acceptance.

Native operating-system drag and drop requires a Wails integration.
