# Host Oiê with authentication

Hosted mode gives one configured OIDC identity full access to one Oiê engine.
API keys have the same engine permissions, including reading history, sending
messages, changing protocol accounts, and resetting engine data. The commands
that manage the owner and keys run on the host as the Oiê service user.

## Register the owner and start the engine

1. Register a confidential web client at your OIDC provider. Set its exact
   redirect URI to `https://oie.example.com/auth/callback`. Obtain the issuer
   URL and the owner's `sub` claim for this client. Some providers assign a
   client-specific subject. Save the client secret in a file readable by the
   Oiê service user.
2. Initialize authentication using the same data directory the server uses.
   This command leaves an existing owner binding in place.

   ```sh
   oie auth init --data-dir /srv/oie/data \
     --issuer https://id.example.com --subject OWNER_SUBJECT
   ```

3. Start Oiê with a certificate for its public hostname and open that HTTPS URL
   in your browser.

   ```sh
   oie --role hosted --listen 0.0.0.0:443 --data-dir /srv/oie/data \
     --public-url https://oie.example.com \
     --tls-cert-file /run/secrets/oie/tls.crt \
     --tls-key-file /run/secrets/oie/tls.key \
     --oidc-client-id OIE_CLIENT_ID \
     --oidc-client-secret-file /run/secrets/oie/client-secret
   ```

Use a hostname dedicated to Oiê. Browsers share cookies across HTTPS ports
on the same hostname. If the service user cannot bind port 443 directly, use a
high port and forward TLS to it, or terminate TLS at a trusted proxy. In proxy mode, replace the certificate and key flags with
`--trusted-tls-proxies 127.0.0.1/32` and bind to `127.0.0.1:8080` when the
proxy shares the host. The proxy must preserve the public `Host` and overwrite
`X-Forwarded-Proto` with `https` on HTTPS requests. For a separate proxy,
allow its actual private source addresses and keep the HTTP hop on a private
network. Forward the full origin, including `/auth/`, and disable buffering
or short response timeouts for SSE. Keep OAuth callback query strings and
authorization headers out of proxy logs.

Local and development BFF roles are for trusted access. Use `--role hosted`
for a public installation. The hosted browser holds a seven-day session and has
a **Sign out** action in Settings. API clients use bearer keys. All engine API
routes require a key, including health, events, and the served API contract.
`--data-only` serves the authenticated API without the browser interface.

## Create and revoke API keys

Run these commands as the service user while the engine is running or stopped.
Use a separate key per client. The default lifetime is 90 days.

```sh
umask 077
oie auth key-create --data-dir /srv/oie/data --name automation --ttl 720h > oie.key
oie auth key-list --data-dir /srv/oie/data
```

The create command prints the secret once. The list command returns key IDs,
labels and expiry times. To use a key with a CLI built from this source:

```sh
OIECTL_TOKEN="$(cat oie.key)" oiectl --server https://oie.example.com system health
```

Other API clients send `Authorization: Bearer <key>` over HTTPS. For the
native desktop client, launch `oie-desktop --engine-url https://oie.example.com
--engine-token-file /path/to/oie.key` with a private key file (mode 0600).

To rotate a key, create a replacement, update the client, then revoke the old
key by ID.

```sh
oie auth key-revoke --data-dir /srv/oie/data --id KEY_ID
```

Revocation blocks new requests. Active streams close on the next credential
check, normally within one second. Accepted sends remain in the engine outbox.

## Recover access

Obtain the replacement issuer and subject, then run:

```sh
oie auth recover --data-dir /srv/oie/data \
  --issuer https://id.example.com --subject REPLACEMENT_SUBJECT
```

Recovery revokes all browser sessions and API keys in the same operation as
changing the owner. Run it with the currently configured identity to revoke
all credentials without changing identity. When changing providers, also
update the client ID and secret and restart the server. The owner and key
state lives in `host-auth/` under the data directory and survives chat data
reset. If you restore a backup of that directory, run recovery before exposing
the server to invalidate credentials from the snapshot.

See the [Helm chart](../charts/oie/README.md#remote-access) for a Kubernetes
deployment behind a TLS-terminating gateway.
