# Message notifications

Settings → This device → Notifications has independent controls for message
banners and sound. Banners start off and request permission when enabled.
Sound starts on for direct messages and for room messages that mention the
account or reply to one of its own retained messages. Archived rooms and the
conversation in focus start silent. The controls let the user turn direct sound
off, choose mentions and replies, all messages or silence for rooms, and enable
sound in archived rooms or the conversation in focus. Preview sound plays the
woodblock and muted marimba cue on demand. WebView and browser autoplay policy
may require an interaction with the app after opening it.

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
profiles and desktop WebViews maintain their own window. Web Audio plays the
bundled 300 ms mono PCM WAV.

## Platform delivery and checks

- Browsers prefer persistent banners through the notification service worker
  while an Oiê page receives events. A click focuses an existing app page and
  takes it to the message, or opens a new window at the message when all pages
  are closed. The new window checks the notification's engine against the
  current engine before loading the conversation. The page-scoped Notifications
  API is a fallback where worker delivery is unavailable.
- Wails sends silent native banners and uses the same Web Audio sound as the
  browser. Closing the desktop window hides it while the app remains running.
  Linux needs a session notification daemon. macOS needs an authorized, valid
  app bundle. Chromium tests observe the banner call and sound playback, and the
  Linux desktop smoke decodes the WAV in its actual WebView. Native banner
  delivery, click-to-open from a hidden window, and macOS authorization still
  need end-to-end coverage with the respective notification centers.
- The standalone manifest lets supported browsers install the app. The open
  PWA uses the same page and notification worker as a browser tab. Install and
  verify permissions, actual notification delivery, click navigation, and
  coordination with a tab on desktop, Android, and iOS. On iOS and iPadOS, test
  on an HTTPS site added to the Home Screen with authorization initiated inside
  the installed app. Do not assume a backgrounded mobile page keeps receiving
  the live stream. Web Push remains the delivery path for a suspended or
  closed page. Its system notification sound will need a separate design
  decision, since the browser Notifications API cannot select Oiê's WAV.

Account Do Not Disturb and shared conversation mutes await the corresponding
account and sync features. Web Push and alerts while Oiê is fully closed are
backlogged.
