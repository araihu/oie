# Message notifications

Settings → This device → Notifications has independent controls for message
banners and the notification sound. Both start off. Oiê asks the browser or OS
for permission when the user turns banners on. Preview sound plays the same
woodblock and muted marimba cue without turning it on. WebView and browser autoplay policy may
require an interaction with the app after opening it.

Conversation details offer a mute on this device. Mutes use the engine and
conversation IDs, work for direct chats and rooms, and also apply to archived
conversations. Message reception and unread counts continue. A conversation that
is visible in a focused window stays silent across tabs in that browser profile.

A notification comes from a new incoming message while Oiê is running. The BFF
reads the engine cursor when opening the event stream and emits it as an
`oie.live` marker without an SSE ID. Message creation at or before that cursor
still updates the timeline and inbox. Only a subsequent incoming creation without
the historical flag can alert. This includes messages for other accounts on the
same engine. Selecting a banner opens the original message. The banner shows a
localized title and the sender's name, keeping the message body in the app.

Cooperating views share the focused conversation via origin-local storage.
IndexedDB serializes a claim by engine and message ID before a window displays
the banner or plays the cue. The claim contains only the IDs and delivery time.
Independent browser profiles and desktop WebViews coordinate alerts within their
own storage. Web Audio plays the bundled 300 ms mono PCM WAV. On browsers that
require a service worker for banners, Oiê registers a worker for those alerts
while the app is open. Desktop banners use the Wails notification service and
require a session notification daemon on Linux or a valid macOS app bundle.

Account Do Not Disturb and shared conversation mutes await the corresponding
account and sync features. Web Push and alerts while Oiê is fully closed are
backlogged.
