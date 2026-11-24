# Local OMEMO direct text (B16 preview)

This increment adds the legacy OMEMO wire format used by Gajim and Conversations
(`eu.siacs.conversations.axolotl`) to an explicitly enabled local engine profile.
Every direct text send in that profile requires verified recipient devices. The
choice survives disconnects, restarts and queued retries. Rooms retain their
usual transport. Use a disposable profile to try it.

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

3. Compare the fingerprint over an authenticated channel. Gajim shows its own identity in the account's OMEMO settings. Conversations shows **OMEMO fingerprint** in **Account details**. Compare all eight groups of hex digits and confirm the bare account address and device ID. Spaces between groups are accepted.

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
in this profile. Encrypted history backup and recovery, key migration, the
per-chat switch, message encryption badges and graphical device verification
remain B16 work. This increment handles live direct text. Encrypted archive
catch-up, copies received from your other devices, direct attachments,
structured replies, edits and reactions are outside its verified scope.
Plaintext direct messages are ignored in this profile.

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
