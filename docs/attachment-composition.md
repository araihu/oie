# Attachment composition

Use the plus menu to pick an image or file. Pasting a file into the composer or
dropping it on the chat pane also adds it directly to the draft. The message box
holds the caption. Each attachment can be removed individually, and supported
images and videos have a thumbnail in the draft. Select the play button on a
video to watch it before sending. Previews wrap onto new rows when space
is limited. Use the composer to send or schedule
the message. Flatpak opens the native chooser for both menu entries. Click a
staged filename to download its original bytes. The desktop app opens the
system save dialog with the file’s name.

## State and effects

| State and action | Result | Draft and focus | Outgoing messages |
| --- | --- | --- | --- |
| Choose, paste or drop files | Show the selected files in the composer and start staging | Make the existing caption available for editing | 0 |
| Remove a file while staging | Cancel that file’s request and remove its row | Keep the typed caption and other files | 0 |
| Unsupported conversation or selection over the attachment count limit | Show a localized notice | Preserve the existing draft | 0 |
| Stage fails or its response is lost | Offer Retry and Remove in that file’s row | Keep the caption and file selection | 0 |
| Retry staging | Reuse the file’s staging ID | Keep the caption editable | 0 |
| Staging succeeds | Retain the file in the scoped draft | Preserve text typed during the request | 0 |
| Send from the composer | Use the ordinary outbox and receipt | Preserve the normal send recovery behavior | 1 logical send |
| Navigation or scheduled-draft switch during staging | Cancel the outstanding selections | A late response cannot apply to another draft | 0 |

Browser staging uses the existing conversation-scoped multipart endpoint. Files stage
one at a time, with an idle timeout for each upload. Send and Schedule
become available after all selected files finish staging or the user
removes the remaining selections. Staged attachments survive a reload.
Thumbnail extraction runs independently of staging and send eligibility.
A restored draft streams its video on explicit playback, including files larger than the thumbnail budget. Unsupported video formats still have filename and removal controls.
Unfinished file selections are held in the current view. The Flatpak chooser
reads selected audio and video locally for the quality dialog. Only the confirmed
attachment is staged. The File and Image actions stage portal-granted files in
the desktop process. A file whose request
committed before cancellation can remain in storage. Cleanup is tracked in O09.

Use **Audio or video** for SD, HD or a compatible original. The device default
is SD. A recording, pasted media or dropped media uses the same quality dialog.
An MP3 with an embedded PNG or JPEG cover qualifies as audio. HD and OG keep
the compatible MP3 when it fits. SD can produce a smaller AAC/M4A with its
cover and descriptive tags, subject to the same 10% savings rule as other
compatible originals. The composer recognizes MP3 by content and corrects the
attachment name and media type when needed. An MP4 video can include a cover.
For an MP3 with a custom cover description, a back cover or separately labeled covers, choose OG to preserve the labels.
Attach a transparent video as a file to keep its alpha channel. An MP4 or WAV detected
as incomplete displays a warning and can only be attached explicitly as a file.

**File** attaches the original. Choosing a quality prepares the bytes on the
device before staging, including for scheduled messages. A draft can contain
up to 16 attachments. The account’s XMPP upload service sets the per-file
limit. Discovery reports a limit only when all eligible services advertise one. The XMPP adapter uploads accepted
attachments before writing the outgoing chat message. The recipient receives
the caption followed by the provider’s download links. A pending message shows
filenames and supported thumbnails while the engine confirms acceptance.

## Voice messages

The recording indicator and timer start when the microphone is live and the
recorder starts. The waveform can appear after recording or playback is already
underway. Playback uses available audio data immediately and adds the seekable
waveform when the format can be decoded within the waveform limits.

After stopping a recording, Send can be selected during the upload. The request
continues in the original conversation if the user switches conversations.
Removing the recording before staging finishes cancels its send. A failed stage
returns the recording for retry in the current app session. Once staging
succeeds, the existing message submission and recovery flow handles the send.

Native operating-system drag and drop requires a Wails integration.
