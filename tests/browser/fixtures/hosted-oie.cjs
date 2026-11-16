const assert = require('node:assert/strict');
const {execFile} = require('node:child_process');
const {X509Certificate, createHash, generateKeyPairSync, randomUUID, sign} = require('node:crypto');
const {once} = require('node:events');
const {readFile, writeFile} = require('node:fs/promises');
const https = require('node:https');
const path = require('node:path');
const {promisify} = require('node:util');
const {request} = require('playwright');
const {loopbackAddress, startOie} = require('./oie-server.cjs');

const run = promisify(execFile);
const clientID = 'browser-fixture';
const clientSecret = 'test-oidc-secret';
const subject = 'test-owner';
const base64json = value => Buffer.from(JSON.stringify(value)).toString('base64url');

async function startIssuer(cert, key, callback) {
  const signing = generateKeyPairSync('rsa', {modulusLength: 2048});
  const publicKey = {...signing.publicKey.export({format: 'jwk'}), alg: 'RS256', use: 'sig', kid: 'fixture'};
  const authorizations = new Map();
  let issuer;
  const server = https.createServer({cert, key}, async (req, res) => {
    const url = new URL(req.url, issuer);
    const json = value => {
      res.writeHead(200, {'Content-Type': 'application/json'});
      res.end(JSON.stringify(value));
    };
    try {
      if (req.method === 'GET' && url.pathname === '/.well-known/openid-configuration') {
        json({
          issuer, authorization_endpoint: issuer + '/authorize?fixture=login',
          token_endpoint: issuer + '/token?fixture=exchange', jwks_uri: issuer + '/keys',
          id_token_signing_alg_values_supported: ['RS256'],
        });
        return;
      }
      if (req.method === 'GET' && url.pathname === '/keys') {
        json({keys: [publicKey]});
        return;
      }
      if (req.method === 'GET' && url.pathname === '/authorize') {
        const query = url.searchParams;
        assert.equal(query.get('fixture'), 'login');
        assert.equal(query.get('client_id'), clientID);
        assert.equal(query.get('redirect_uri'), callback);
        assert.equal(query.get('code_challenge_method'), 'S256');
        const code = randomUUID();
        authorizations.set(code, {nonce: query.get('nonce'), challenge: query.get('code_challenge')});
        res.writeHead(303, {Location: callback + '?' + new URLSearchParams({code, state: query.get('state')})});
        res.end();
        return;
      }
      if (req.method === 'POST' && url.pathname === '/token') {
        assert.equal(url.searchParams.get('fixture'), 'exchange');
        assert.equal(req.headers.authorization, 'Basic ' + Buffer.from(clientID + ':' + clientSecret).toString('base64'));
        const chunks = [];
        for await (const chunk of req) chunks.push(chunk);
        const form = new URLSearchParams(Buffer.concat(chunks).toString());
        const code = form.get('code');
        const attempt = authorizations.get(code);
        authorizations.delete(code);
        assert.equal(form.get('redirect_uri'), callback);
        assert.equal(createHash('sha256').update(form.get('code_verifier')).digest('base64url'), attempt?.challenge);
        const now = Math.floor(Date.now() / 1000);
        const header = base64json({alg: 'RS256', kid: publicKey.kid});
        const body = base64json({iss: issuer, aud: clientID, sub: subject, nonce: attempt.nonce, iat: now, exp: now + 60});
        const payload = header + '.' + body;
        const sig = sign('sha256', Buffer.from(payload), signing.privateKey).toString('base64url');
        json({access_token: 'fixture', token_type: 'Bearer', id_token: payload + '.' + sig});
        return;
      }
      res.writeHead(404).end();
    } catch {
      res.writeHead(400).end();
    }
  });
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  issuer = 'https://localhost:' + server.address().port;
  return {url: issuer, stop: () => new Promise(resolve => server.close(resolve))};
}

async function startHostedOie(binary) {
  let issuer, server, api, key, certificatePin;
  try {
    server = await startOie(binary, [], '/readyz', async ({listen, dir}) => {
      const url = 'https://' + listen;
      const cert = path.join(dir, 'tls.crt');
      const tlsKey = path.join(dir, 'tls.key');
      const credentials = path.join(dir, 'oidc-client-secret');
      await run('openssl', ['req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-days', '1',
        '-subj', '/CN=localhost', '-addext', 'subjectAltName=DNS:localhost,IP:127.0.0.1',
        '-out', cert, '-keyout', tlsKey]);
      await writeFile(credentials, clientSecret, {mode: 0o600});
      const pem = await readFile(cert);
      const publicKey = new X509Certificate(pem).publicKey.export({type: 'spki', format: 'der'});
      certificatePin = createHash('sha256').update(publicKey).digest('base64');
      issuer = await startIssuer(pem, await readFile(tlsKey), url + '/auth/callback');
      const authEnv = {...process.env, OIE_DATA_DIR: dir, OIE_OIDC_ISSUER: issuer.url, OIE_OIDC_SUBJECT: subject};
      await run(binary, ['auth', 'init'], {env: authEnv});
      key = (await run(binary, ['auth', 'key-create', '--name', 'browser-fixture'], {env: authEnv})).stdout.trim();
      const probe = await loopbackAddress();
      return {
        url, readyOrigin: 'http://' + probe,
        env: {
          ...process.env, SSL_CERT_FILE: cert,
          OIE_ROLE: 'hosted', OIE_PUBLIC_URL: url, OIE_PROBE_LISTEN: probe,
          OIE_TLS_CERT_FILE: cert, OIE_TLS_KEY_FILE: tlsKey, OIE_TRUSTED_TLS_PROXIES: '',
          OIE_OIDC_CLIENT_ID: clientID, OIE_OIDC_CLIENT_SECRET_FILE: credentials,
        },
      };
    });
    api = await request.newContext({
      baseURL: server.url, ignoreHTTPSErrors: true, extraHTTPHeaders: {Authorization: 'Bearer ' + key},
    });
    return {
      ...server, api, certificatePin,
      async signIn(page, target) {
        await page.goto(server.url + target);
        await page.getByRole('link', {name: /^(Sign in|Entrar)$/}).click();
        await page.waitForURL(server.url + target);
      },
      async stop() {
        await api.dispose();
        await server.stop();
        await issuer.stop();
      },
    };
  } catch (error) {
    await api?.dispose();
    await server?.stop();
    await issuer?.stop();
    throw error;
  }
}

module.exports = {startHostedOie};
