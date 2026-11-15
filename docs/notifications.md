# Message notifications

Settings → This device → Notifications has independent controls for message
banners and sound. Banners start off and request permission when enabled.
Sound starts on for direct messages and for room messages that mention the
account or reply to one of its own retained messages. Archived rooms and the
conversation in focus start silent. The controls let the user turn direct sound
off, choose mentions and replies, all messages or silence for rooms, and enable
sound in archived rooms or the conversation in focus. The built-in Oiê tone
uses a woodblock and muted marimba. Choose file accepts an audio file the
device can decode, up to 5 MB. Oiê takes the first two seconds. The play
button previews the selected tone, even with notification sound off. The X
beside the custom filename restores the built-in sound.

Custom audio stays on this device. The WebView or browser decodes it and
saves a short mono PCM WAV in IndexedDB. Cooperating views share the choice
within their browser profile and app origin. Browser and Linux WebView
autoplay policy may require an interaction with the app after opening it.

Conversation details offer a mute on this device. Mutes use the engine and
conversation IDs and apply to direct chats and rooms, including archived
conversations. Message reception and unread counts continue.

A notification comes from a new incoming message while Oiê is running. The BFF
reads the engine cursor when opening the event stream and emits it as an
`oie.live` marker without an SSE ID. Message creation at or before that cursor
still updates the timeline and inbox. Only a subsequent incoming creation without
the historical flag can alert. This includes messages for other accounts on the
same engine. Selecting a banner opens the original message. The banner shows a
localized title and the sender's name, keeping the message body in the app.

Cooperating views share the focused conversation via origin-local storage.
The browser requests current conversation and archive information on demand
from the BFF, which uses the existing engine API and checks the original
message's ownership for replies. Identical concurrent lookups share one
request per window. IndexedDB serializes separate banner and sound claims by
engine and message. It also stores the last sound time in the same transaction.
The first eligible message plays immediately once its context is resolved.
Further arrivals within two seconds are skipped, even with multiple tabs,
and no sound is scheduled for the end of the window. Independent browser
profiles and desktop WebViews maintain their own window. The bundled tone is
a 300 ms mono PCM WAV clip.

## Platform delivery and checks

- Browsers prefer persistent banners through the notification service worker
  while an Oiê page receives events. A click focuses an existing app page and
  takes it to the message, or opens a new window at the message when all pages
  are closed. The new window checks the notification's engine against the
  current engine before loading the conversation. The page-scoped Notifications
  API is a fallback where worker delivery is unavailable.
- Wails sends silent native banners. On macOS, AppKit plays the selected WAV for
  previews and for incoming messages, including with the window hidden. On
  Linux, Web Audio plays the same WAV. Closing the desktop window hides it
  while the app remains running.
  The Flatpak uses a native file chooser to read the selected audio through
  the Documents portal.
  Linux needs a session notification daemon. macOS needs an authorized, valid
  app bundle. The Linux desktop smoke decodes the WAV in its actual WebView,
  sends a native banner through a private D-Bus notification daemon, and
  verifies that clicking restores the hidden window at the exact message.
  A macOS user verified on rc.20 that the banner restores the hidden window at
  the exact message and confirmed sound playback on rc.21.
- The standalone manifest lets supported browsers install the app. The open
  PWA uses the same page and notification worker as a browser tab. On Linux,
  Chromium 151 installed Oiê through its UI and opened the exact message in the
  standalone window for a D-Bus notification click, both with the window open
  and after closing it. Permissions, delivery, navigation, and coordination
  with a tab still need coverage on Android and iOS. On iOS and iPadOS, test
  on an HTTPS site added to the Home Screen with authorization initiated inside
  the installed app. Do not assume a backgrounded mobile page keeps receiving
  the live stream. Web Push remains the delivery path for a suspended or
  closed page. Its system notification sound will need a separate design
  decision, since the browser Notifications API cannot select Oiê's WAV.

### Testing on another device

On the test host, run `make build` and `node tests/browser/notifications.cjs
--manual`. The script starts a loopback BFF and a fake engine with disposable
conversations. Use the printed web URL behind an HTTPS reverse proxy for a
mobile device. On a Mac, launch the test build of the app bundle with the
printed `--engine-url` and a temporary `--data-dir` on the same host.

Open **Conversation 0**, enable message banners, and grant permission when
prompted. On mobile, do this in the installed app. Enter a label in the test
terminal to send a message to **Conversation 1**. Click the banner while Oiê
is open and check that the label appears in the destination conversation.
Return to **Conversation 0**, send a different label, and leave its banner in
the notification center. Hide the desktop window or close the PWA window,
then click the existing banner and verify that it opens the second message.

Account Do Not Disturb and shared conversation mutes await the corresponding
account and sync features. Web Push and alerts while Oiê is fully closed are
backlogged.
