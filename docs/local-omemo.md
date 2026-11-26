# OMEMO for direct chats and private rooms (B16 preview)

This increment adds the legacy OMEMO wire format used by Gajim and Conversations
(`eu.siacs.conversations.axolotl`) to local and hosted engines. Enable it once
per profile. Direct messages require verified recipient devices by default. In conversation details, **OMEMO encryption**
opens the device list and the encryption control. Changing the control applies
to future messages. A queued encrypted message keeps its protection through
retries and restarts.

In hosted mode the server is one trusted device per XMPP account. Every
authenticated browser uses that device and shares its fingerprint, verification
decisions and readable history. Other XMPP clients verify the server's fingerprint
once. The server operator can read the messages. Use the existing
[hosted authentication and HTTPS setup](hosted-auth.md).

## Try it

1. Build the engine with `make build`. Initialize a profile while its engine is stopped, then launch it.

   ```sh
   export OIE_DATA_DIR="$(mktemp -d)"
   ./bin/oie omemo init
   ./bin/oie --listen 127.0.0.1:8080
   ```

2. Add and connect an XMPP account in Oiê. Open a direct chat and send a short message. It stays queued while a recipient needs verification. In a second terminal, select the same `OIE_DATA_DIR`. Find the account UUID with `oiectl` or `GET /api/v1/accounts`. Display this device's public fingerprint and the keys discovered during sending or receipt.

   ```sh
   ./bin/oie omemo devices --account "$ACCOUNT_ID"
   ```

3. Compare the fingerprint over an authenticated channel. Gajim shows its own identity in the account's OMEMO settings. Conversations shows **OMEMO fingerprint** in **Account details**. In Oiê, open the OMEMO page for the account to share its fingerprint or QR code. Open it from a direct conversation to enter a fingerprint, paste an XMPP verification link, or upload its QR image (PNG or JPEG). Review the address, device ID and fingerprint before confirming. You can also verify from the CLI. Compare all eight groups of hex digits and confirm the bare account address and device ID. Spaces between groups are accepted.

   ```sh
   ./bin/oie omemo verify --account "$ACCOUNT_ID" --peer "$PEER_JID" \
     --device "$DEVICE_ID" --fingerprint "$FINGERPRINT"
   ```

4. Repeat for each published recipient device and each other device of your own account. Oiê then retries the queued message. Compare Oiê's local fingerprint on the other client as well. To withdraw a device's approval, run the following command. Subsequent direct sends wait for verification while that device is advertised.

   ```sh
   ./bin/oie omemo distrust --account "$ACCOUNT_ID" --peer "$PEER_JID" \
     --device "$DEVICE_ID"
   ```

## What this profile stores

Each account owns an independent identity, signed prekey, one-time prekeys,
session ratchets and fingerprint approvals under the private profile directory.
The engine fetches PEP device lists and bundles and checks the identity for
each recipient before encrypting. A changed observed identity invalidates its
approval and session. The engine holds live incoming ciphertext awaiting
verification in arrival order, bounded to 128 envelopes and 2 MiB per account.
A newly verified sender's held messages can then enter the timeline.

The local timeline, search index and outgoing queue can contain readable text.
Use OS account permissions and disk encryption to protect the profile.
Automatic link preview fetching and conventional Oiê ZIP export are disabled
in this profile. Encrypted direct messages have an OMEMO label. The engine
accepts unencrypted direct text only when the user explicitly enables it for
that peer. Replies in a protected conversation include the quote in the
encrypted text. Room mentions appear as text. Protocol edits and reactions
are unavailable for protected messages and conversations.

## Send and download encrypted attachments

Use the attachment picker in a chat with OMEMO enabled. Oiê supports
encrypted attachments up to 20 MiB. The provider's limit applies to the
ciphertext, including its authentication tag.
Oiê encrypts each file before sending its bytes to the XMPP HTTP Upload
service. The provider sees the upload filename and ciphertext size. The key
and URL travel inside an OMEMO message. A caption and each attached file
arrive as separate encrypted messages in Gajim and Conversations.
[XEP-0363](https://xmpp.org/extensions/xep-0363.html) ·
[XEP-0454](https://xmpp.org/extensions/xep-0454.html).

Select a received filename to download it. The engine verifies the entire
file before returning it. Local or hosted Oiê can also download files
recovered from the XMPP archive while the upload service still has the bytes.
The hosting operator can read files processed by the hosted engine.

Ciphertext uploads and downloads use HTTPS. For a dedicated private HTTPS upload service,
set `--omemo-upload-origin https://upload.example.internal:5443` or
`OIE_OMEMO_UPLOAD_ORIGIN` on the engine. The exception applies to that exact
host and port. To trust a private CA, supply the CA with `--xmpp-ca-file`.
Encrypted transfers do not follow redirects.

## Recover direct messages from the XMPP server

On connection or reconnection, the local or hosted engine retrieves encrypted
direct text from the XMPP account's archive (XEP-0313). The engine needs this
device's session or prekeys and the verified sender identity. Recovered messages
retain their archive timestamp and appear in the conversation with the OMEMO
label. Live delivery and archive replay share the same message identity.

If Oiê cannot decrypt an archived message, the account reports that part of its
history is unavailable. The engine continues through later messages and retries
from the gap on subsequent passes. Verify an unfamiliar sender's fingerprint in
the OMEMO settings to let Oiê retry. An archive entry encrypted exclusively for
another device, or whose message key has been lost, remains unavailable. A fresh
installation or a private identity export alone may have no usable session for
old messages. A password-protected Oiê history backup can recover previously
retained text in those cases. The server's archive retention controls which
records it can return. Oiê also applies its own retention policy.

The archive import covers the legacy OMEMO namespace. In an OMEMO profile this
pass selects encrypted direct messages. Rooms use their separate archive path.

## Turn on OMEMO in a private room

The room must restrict entry to members and let participants see one another's
XMPP addresses. Join the room and open **OMEMO encryption** in its details.
Select **Enable OMEMO in this group** and check the devices listed for each
member. Verify every active device. The local and hosted engines encrypt for
the complete membership, including members who are offline. The hosted engine
uses the server identity, as in direct chats.

The engine checks the configuration and current affiliations before sending.
A queued encrypted message stays encrypted through retries. If the room
configuration changes or a member has an unverified device, the encrypted send
waits. The sender can still cancel while the message is waiting for verification
before upload or transmission. This preview supports up to 64 members and 256
recipient devices.
Other room clients each control their own encryption setting. After importing
an identity into a new profile, enable OMEMO for each private room again.
Disabling OMEMO pauses encrypted reception and recovery in that room. Enabling
it again resumes recovery from a compatible archive.

The legacy format identifies each recipient key by device ID. If devices on
different accounts publish the same ID, Oiê holds the send because some
clients can read only one recipient key per ID. If the collision involves a
retired device, its owner can clear the stale device from their published
device list using their client. If both devices are active, use a different
enrolled device or re-enroll one device after saving its history. Retire the
colliding ID and verify any new fingerprint before retrying the protected send.

Private rooms support encrypted text and files while joined. Compatible
archives can return encrypted messages after reconnecting, provided the archive
supplies the sender's original affiliation and XMPP address and Oiê has the
keys. Oiê uses the archive timestamp. An unavailable message can hold the
affected archive range open for retry after verification while the rest of
history is recovered. Encrypted messages addressed only to other devices stay
unavailable on this installation. Oiê also processes archived room withdrawals
that refer to an authenticated message. The server needs to supply its stable
message IDs for those references.
Search uses the content recovered by Oiê.
[XEP-0384](https://xmpp.org/extensions/xep-0384.html#usecases-muc) ·
[XEP-0045](https://xmpp.org/extensions/xep-0045.html).

## Migrate an identity or recover history

Stop the source engine. Set `OIE_DATA_DIR` to its profile. The commands below
prompt for a passphrase, or accept `--passphrase-file` for automation.

```sh
oie omemo export-key --account "$ACCOUNT_ID" --file my-identity.age
oie omemo export-history --file my-history.age
oie omemo inspect-key --file my-identity.age
```

On the destination, initialize OMEMO with the engine stopped. Restore history
to create the saved accounts and conversations, or register the same XMPP
account before connecting if you only need its identity. Use the destination's
account UUID and compare the fingerprint shown by `inspect-key` with the source.

```sh
oie omemo init
oie omemo import-history --file my-history.age
oie omemo import-key --account "$ACCOUNT_ID" --file my-identity.age --fingerprint "$FINGERPRINT"
```

Retire the source before connecting the restored account. Re-enter its XMPP
credentials and verify contacts again. Oiê retains the device ID and identity,
creates fresh prekeys and negotiates new sessions. If a verified peer's session
needs repair, compare its fingerprint and run the following command, then send
a message to establish a fresh session.

```sh
oie omemo repair --account "$ACCOUNT_ID" --peer "$PEER_JID" --device "$DEVICE_ID" --fingerprint "$FINGERPRINT"
```

Both files use the age v1 passphrase format. The authenticated plaintext begins
with `OIE OMEMO IDENTITY 1\n` or `OIE OMEMO HISTORY 1\n`. The identity payload
is JSON containing the bare address, numeric device ID, hex fingerprint and
base64-encoded 32-byte private key. The history payload is the versioned Oiê
backup ZIP for retained profile history, including other conversations. Media
files, protocol credentials and private OMEMO keys are excluded. Import merges
with retained data and pauses restored sends. Only retained readable messages
can be recovered this way. Gajim exports and Conversations .ceb backups use
different formats.

## Reproduce the interoperability check

Use a TLS-enabled XMPP server with PEP, three disposable accounts and a
distinct device for each client. Enable OMEMO in both peers. For a private test
CA, pass `--xmpp-ca-file` to Oiê and trust the test certificate in the peer
clients. Exercise each row with unique text.

| Direction | Receiving evidence from the local Prosody fixture |
| --- | --- |
| Oiê → Gajim 2.4.3 | Gajim stored the exact text with OMEMO encryption metadata and the Oiê device fingerprint. |
| Gajim 2.4.3 → Oiê | Gajim sent through its OMEMO message path. Oiê's conversation API returned the decrypted reply as received. |
| Oiê → Conversations 2.21.4 | The installed Android client displayed the text and identified the chat as encrypted with OMEMO. |
| Conversations 2.21.4 → Oiê | A reply from the Android encrypted composer appeared in Oiê's conversation API as received. |

Check the server's archive for encrypted envelopes and search it for the unique
test strings. Revoke the Gajim device, submit a fresh message, and verify it
stays queued. Compare and verify the fingerprint again. The same message ID
should become submitted and Gajim should record the text as OMEMO.

In the fixture, Prosody 13.0.4 archived nine encrypted messages for the Oiê
account across both exchanges, including the post-restart replies and the
held send. None of the nine test strings appeared in any of the three server
archives. Both peers displayed new encrypted text after restarting the Oiê
engine with the same profile.

The restored hosted fixture exposed the original device ID and fingerprint to
two independent owner credentials. Two isolated Chromium browser contexts (desktop
and mobile viewports) also signed in independently via the hosted OIDC flow.
Both displayed the same fingerprint and recovered conversation. Both XMPP clients received a new encrypted message
from that device and sent an encrypted reply that the other owner credential
could read. Gajim recorded OMEMO on both messages. Conversations displayed the
incoming text and sent from its encrypted composer. A second history import added
zero messages after the first restored nine. An altered history archive and a
wrong passphrase both failed before a destination database was created.

In a direct conversation with Gajim, a new protected send stayed queued through
a switch to plaintext. Reverification released the original message. The Prosody
archives contained the separate explicitly authorized plaintext control and
none of the protected text from the hosted exchanges.

For XMPP archive recovery, Prosody 13.0.4 ran with offline delivery disabled.
With Oiê stopped, Gajim 2.4.3 and Conversations 2.21.4 each sent an encrypted
direct message. The local engine recovered both when it connected. The test
then repeated both sends with the hosted engine stopped. An authenticated
hosted session read all four archived messages once, with the original Oiê
IDs preserved on replay. An additional Gajim message received live retained
its ID after a restart and archive catch-up. The five message bodies were
absent from the Prosody archive.


For encrypted attachments, Gajim 2.4.3 and Conversations 2.21.4+free on
Android each sent and received a fixture PNG with both engine roles. Every
download matched its original by SHA-256 or byte comparison. The hosted
engine recovered both inbound attachment messages from XMPP history after
the clients sent them while it was stopped. A Chromium browser downloaded
the correct original files in both roles. Prosody stored eight encrypted
file blobs, and its XMPP archive contained no download URI or key.

The private room fixture used Prosody 13.0.4 with a members-only room,
visible XMPP addresses and MAM. Gajim 2.4.3 and Conversations 2.21.4+free
both exchanged live encrypted text with the local and hosted engines.
The hosted engine also recovered both clients' messages sent while Oiê was
stopped, and Chromium showed one OMEMO-badged message per archived reply.
Both clients matched the bytes of an encrypted PNG sent by hosted Oiê.
Hosted Oiê matched the bytes of an encrypted PNG sent by Gajim. Changing the room to
show addresses only to moderators held the next protected send in the
queue. Restoring visible addresses delivered the same message with OMEMO.
The room archive stored encrypted envelopes without the protected text
or attachment links.
