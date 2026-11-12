const assert = require('node:assert/strict');
const http = require('node:http');
const fs = require('node:fs/promises');
const path = require('node:path');
const {spawn} = require('node:child_process');
const {createHash} = require('node:crypto');
const {once} = require('node:events');
const {chromium} = require('playwright');

const id = n => '019a5df0-0000-7000-8000-' + String(n).padStart(12, '0');
const engine = id(1), alice = id(2), bob = id(3), chat = id(4), room = id(5), revision = id(6);
const at = '2026-11-12T12:00:00Z';
const address = 'socializando-saberes@salas.suchat.org';
const invitation = 'xmpp:' + address + '?join';
const avatar = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAgAAAAICAYAAADED76LAAAAEklEQVR4nGPIiJrzHx9mGBkKAMGrl0HBdBrIAAAAAElFTkSuQmCC', 'base64');
const avatarHash = createHash('sha256').update(avatar).digest('hex');
const source = {id: chat, revisionId: revision, accountId: alice, kind: 'direct', address: 'friend@fixture',
  title: 'Friend', updatedAt: at, unreadCount: 0, archived: false, favorite: false};
const accounts = [
  {id: alice, revisionId: revision, label: 'Alice', address: 'alice@fixture', protocol: 'xmpp', createdAt: at},
  {id: bob, revisionId: revision, label: 'Bob', address: 'bob@fixture', protocol: 'xmpp', createdAt: at}
];
const previews = [], commands = [], streams = new Set();
let held, destination, roomState = 'joining', cursor = 0, conflict = false;

async function apiHandler(req, res) {
  const u = new URL(req.url, 'http://fixture');
  const send = (status, data) => {
    res.writeHead(status, {'Content-Type': 'application/json'});
    res.end(JSON.stringify(data));
  };
  if (u.pathname === '/api/v1/engine') return send(200, {id: engine, role: 'hosted', apiVersion: '0.27.0', eventCursor: engine + ':' + cursor});
  if (u.pathname === '/api/v1/accounts') return send(200, {items: accounts});
  if (u.pathname === '/api/v1/events') {
    res.writeHead(200, {'Content-Type': 'text/event-stream'});
    res.write(': ready\n\n');
    streams.add(res);
    req.on('close', () => streams.delete(res));
    return;
  }
  if (u.pathname === '/api/v1/avatars/' + avatarHash) {
    res.writeHead(200, {'Content-Type': 'image/png'});
    res.end(avatar);
    return;
  }
  if (u.pathname.endsWith('/room-preview')) {
    previews.push({account: u.pathname.split('/')[4], address: u.searchParams.get('address')});
    if (held) await held;
    if (u.searchParams.get('address') === 'missing@fixture') return send(200, {address: 'missing@fixture', state: 'not_found'});
    return send(200, {address, state: 'known', name: 'Saberes <script>', description: 'Bem-vindos!', topic: 'Encontro', occupantCount: 17, avatarHash});
  }
  if (req.method === 'POST' && u.pathname.endsWith('/rooms')) {
    let data = '';
    for await (const chunk of req) data += chunk;
    const accountId = u.pathname.split('/')[4];
    const request = JSON.parse(data);
    commands.push({accountId, key: req.headers['idempotency-key'], ...request});
    if (conflict) return send(409, {code: 'state_conflict', status: 409});
    destination = {...source, id: room, accountId, address: request.address, kind: 'room', title: request.name || request.address};
    roomState = 'joining';
    return send(202, destination);
  }
  if (u.pathname.endsWith('/connection')) return send(200, {accountId: u.pathname.split('/')[4], state: 'connected', updatedAt: at});
  if (u.pathname.endsWith('/capabilities')) return send(200, {accountId: u.pathname.split('/')[4], state: 'known',
    features: [{id: 'room_messaging', appSupport: 'partial', protocolSupport: 'supported', serverSupport: 'scope_dependent'}]});
  if (u.pathname.endsWith('/history')) return send(200, {state: 'complete', retainedMessages: 1, conversations: 1, updatedAt: at});
  if (u.pathname.endsWith('/avatars') || u.pathname.endsWith('/previews') || u.pathname.endsWith('/message-stars')) return send(200, {items: []});
  if (u.pathname === '/api/v1/conversations') return send(200, {items: [source, destination].filter(c => c && (!u.searchParams.has('accountId') || c.accountId === u.searchParams.get('accountId')))});
  if (u.pathname === '/api/v1/conversations/' + chat) return send(200, source);
  if (u.pathname === '/api/v1/conversations/' + room && destination) return send(200, destination);
  if (u.pathname.endsWith('/room-session') && destination) return send(200, {accountId: destination.accountId,
    conversationId: room, active: true, state: roomState, intentRevisionId: revision, updatedAt: at});
  if (u.pathname === '/api/v1/conversations/' + chat + '/messages') return send(200, {items: [{id: id(8), revisionId: revision,
    accountId: alice, conversationId: chat, sender: {id: id(9), displayName: 'Friend'}, originalText: 'Convite: ' + invitation,
    createdAt: at, isOwn: false, sendState: 'received'}]});
  if (u.pathname.endsWith('/messages')) return send(200, {items: []});
  return send(404, {status: 404});
}

async function waitFor(predicate) {
  for (let i = 0; i < 160; i++) {
    if (await predicate()) return;
    await new Promise(resolve => setTimeout(resolve, 50));
  }
  throw Error('Timed out: ' + predicate);
}

(async () => {
  const api = http.createServer((req, res) => { apiHandler(req, res).catch(error => res.destroy(error)); });
  api.listen(0, '127.0.0.1');
  await once(api, 'listening');
  const allocation = http.createServer();
  allocation.listen(0, '127.0.0.1');
  await once(allocation, 'listening');
  const base = 'http://127.0.0.1:' + allocation.address().port;
  await new Promise(resolve => allocation.close(resolve));
  const child = spawn(process.env.OIE_WEB_BINARY || path.resolve(__dirname, '../../bin/oie'),
    ['--role', 'bff', '--listen', new URL(base).host, '--engine-url', 'http://127.0.0.1:' + api.address().port], {stdio: ['ignore', 'ignore', 'inherit']});
  const browser = await chromium.launch();
  const errors = [];
  let release;
  try {
    await waitFor(async () => { try { return (await fetch(base)).ok; } catch { return false; } });
    const page = await browser.newPage({viewport: {width: 1440, height: 950}, locale: 'pt-BR'});
    page.on('pageerror', error => errors.push(error.message));
    held = new Promise(resolve => { release = resolve; });
    await page.goto(base + '/?' + new URLSearchParams({chat, account: alice, lang: 'pt-BR'}));
    const draft = page.locator('#message-composer');
    await draft.fill('Meu rascunho');
    const link = page.locator('.oie-rendered-message [data-room-invitation]');
    await link.waitFor();
    assert.equal(await link.textContent(), invitation);
    const card = page.locator('[data-room-bubble]');
    await card.getByText('Convite para sala', {exact: true}).waitFor();
    await waitFor(() => previews.length === 1);
    assert.equal(previews[0].account, alice);
    assert.equal(previews[0].address, address);
    assert.equal(commands.length, 0);
    const foreign = await page.request.post(base + '/ui/rooms/preview', {form: {accountId: alice, address}});
    assert.equal(foreign.status(), 403);

    await page.evaluate(() => {
      const elements = ['#send-message-form', '.oie-timeline-frame', '#message-timeline', '[data-room-bubble]', '.oie-room-invitation-action'].map(s => document.querySelector(s));
      const sample = () => elements.map(el => {
        const box = el.getBoundingClientRect();
        return [box.x, box.y, box.width, box.height, el.scrollTop];
      }).flat();
      const initial = sample();
      window.roomDrift = 0;
      window.trackRoomGeometry = true;
      function frame() {
        for (const [index, value] of sample().entries()) window.roomDrift = Math.max(window.roomDrift, Math.abs(value - initial[index]));
        if (window.trackRoomGeometry) requestAnimationFrame(frame);
      }
      requestAnimationFrame(frame);
    });
    release();
    held = undefined;
    await card.getByText('Saberes <script>', {exact: true}).waitFor();
    await card.getByText('Bem-vindos!', {exact: true}).waitFor();
    await waitFor(() => card.locator('img').evaluate(img => img.complete && img.naturalWidth > 0));
    assert.equal(await card.locator('script').count(), 0);
    previews.length = 0;
    held = new Promise(resolve => { release = resolve; });
    await link.focus();
    await page.keyboard.press('Enter');
    const form = page.locator('#join-room-form');
    const preview = form.locator('[data-room-preview]');
    await preview.getByText('Carregando prévia da sala…').waitFor();
    assert.equal(new URL(page.url()).searchParams.get('chat'), chat);
    assert.equal(new URL(page.url()).searchParams.has('open'), false);
    assert.equal(await form.locator('[name=address]').inputValue(), address);
    assert.equal(await form.locator('[name=accountId]').inputValue(), alice);
    assert.equal(commands.length, 0);
    await form.getByRole('combobox').click();
    await form.getByRole('option', {name: 'Bob · bob@fixture'}).click();
    await waitFor(() => previews.length === 2);
    assert.deepEqual(previews.map(value => value.account), [alice, bob]);
    release();
    held = undefined;
    await preview.getByText('Saberes <script>', {exact: true}).waitFor();
    assert.equal(await preview.locator('script').count(), 0);
    await waitFor(() => preview.locator('img').evaluate(img => img.complete && img.naturalWidth > 0));
    assert.equal(await preview.locator('img').getAttribute('src'), '/ui/avatars/' + avatarHash);
    assert.equal(await page.evaluate(() => { window.trackRoomGeometry = false; return window.roomDrift; }), 0);
    await page.keyboard.press('Escape');
    await waitFor(() => link.evaluate(el => document.activeElement === el));
    assert.equal(await draft.inputValue(), 'Meu rascunho');
    assert.equal(commands.length, 0);

    await card.getByRole('link', {name: 'Ver sala'}).click();
    await preview.getByText('Bem-vindos!').waitFor();
    await form.getByRole('combobox').click();
    await form.getByRole('option', {name: 'Bob · bob@fixture'}).click();
    await form.locator('[name=nickname]').fill('Leitora');
    const joined = page.waitForResponse(res => res.url() === base + '/ui/rooms/open');
    await form.getByRole('button', {name: 'Entrar na sala', exact: true}).click();
    const joinResponse = await joined;
    assert.equal(joinResponse.status(), 200, await joinResponse.text());
    await page.waitForURL(url => url.searchParams.get('chat') === room).catch(async error => {
      throw new Error(JSON.stringify({url: page.url(), response: await joinResponse.json(), commands, errors}), {cause: error});
    });
    await page.locator('#room-session-status[data-room-state=joining]').waitFor();
    assert.deepEqual(commands.map(({key, ...command}) => command), [{accountId: bob, address, action: 'join', nickname: 'Leitora', name: ''}]);
    roomState = 'joined';
    cursor++;
    for (const stream of streams) stream.write('id: ' + engine + ':' + cursor + '\nevent: conversation.room_session_changed\ndata: {}\n\n');
    await page.locator('#room-session-status').waitFor({state: 'hidden'});

    conflict = true;
    await page.goto(base + '/?' + new URLSearchParams({chat, account: alice, lang: 'en'}));
    await page.locator('body[data-oie-live-ready=true]').waitFor();
    await page.locator('[data-new-conversation]').click();
    await page.locator('#newchat-body [data-room-entry-open=create-room]').click();

    const create = page.locator('#create-room-form');
    await create.locator('[name=address]').fill('new@rooms.fixture');
    await create.locator('[name=name]').fill('A new room');
    await create.getByRole('button', {name: 'Create room'}).click();
    await create.getByText('Room state changed. Refresh before trying again.').waitFor();
    assert.equal(await create.locator('[name=name]').inputValue(), 'A new room');
    conflict = false;
    await create.getByRole('button', {name: 'Create room'}).click();
    await page.waitForURL(url => url.searchParams.get('chat') === room);
    assert.equal(commands[1].key, commands[2].key);
    assert.equal(commands[2].action, 'create');
    assert.equal(commands[2].name, 'A new room');

    await page.goto(base + '/?' + new URLSearchParams({chat, account: alice, open: 'join-room', lang: 'en'}));
    await form.waitFor();
    const previewButton = form.getByRole('button', {name: 'Preview room'});
    await form.locator('[name=address]').fill('missing@fixture');
    await previewButton.click();
    await preview.getByText('Room not found.', {exact: true}).waitFor();
    const csrf = await page.locator('meta[name=oie-csrf]').getAttribute('content');
    const beforeStale = previews.length;
    const stale = await page.request.post(base + '/ui/rooms/preview', {form: {accountId: alice, address, csrf, engineId: id(15)}});
    assert.equal(stale.status(), 503);
    assert.equal(previews.length, beforeStale);
    await form.locator('[name=address]').fill(invitation);
    await page.route('**/ui/rooms/preview', route => route.abort(), {times: 1});
    await previewButton.click();
    await preview.getByText('Room preview unavailable.', {exact: true}).waitFor();
    assert.equal(await form.locator('[name=address]').inputValue(), invitation);
    await previewButton.click();
    await preview.getByText('Bem-vindos!').waitFor();
    assert.equal(previews.at(-1).address, address);
    assert.equal(commands.length, 3);

    const fallback = await browser.newPage({viewport: {width: 390, height: 860}, locale: 'en'});
    fallback.on('pageerror', error => errors.push(error.message));
    await fallback.route('**/ui/rooms/preview', route => {
      const form = new URLSearchParams(route.request().postData());
      return form.get('presentation') === 'message' ? route.fulfill({status: 204}) : route.continue();
    });
    await fallback.goto(base + '/?' + new URLSearchParams({chat, account: alice, lang: 'en'}));
    await fallback.locator('[data-room-bubble]').getByText('Room invitation', {exact: true}).waitFor();
    await fallback.locator('[data-room-bubble]').getByRole('link', {name: 'View room'}).click();
    await fallback.locator('#join-room-form').getByText('Bem-vindos!').waitFor();
    assert.equal(commands.length, 3);
    await fallback.close();

    const artifacts = path.join(__dirname, 'artifacts');
    await fs.mkdir(artifacts, {recursive: true});

    for (const width of [390, 1440]) {
      for (const theme of ['goshtoso', 'minimal']) {
        for (const mode of ['light', 'dark']) {
          const view = await browser.newPage({viewport: {width, height: 900}, locale: 'en'});
          view.on('pageerror', error => errors.push(error.message));
          await view.goto(base + '/?' + new URLSearchParams({chat, account: alice, open: 'join-room', room: address, theme: mode, lang: 'en'}));
          await view.evaluate(value => { document.documentElement.dataset.theme = value; }, theme);
          await view.locator('#join-room-form').getByText('Bem-vindos!').waitFor();
          await view.locator('[data-room-bubble]').getByText('Saberes <script>', {exact: true}).waitFor();
          assert(await view.evaluate(() => document.documentElement.scrollWidth <= innerWidth));
          await view.addScriptTag({path: require.resolve('axe-core/axe.min.js')});
          const scan = id => view.evaluate(selector => window.axe.run(selector,
            {runOnly: {type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa']}}), id);
          const joinedIssues = await scan('#join-room-body');
          assert.deepEqual(joinedIssues.violations.map(value => value.id), [], ['join', width, theme, mode].join(' '));
          await view.screenshot({path: path.join(artifacts, `room-join-${width}-${theme}-${mode}.png`), animations: 'disabled'});
          await view.keyboard.press('Escape');
          await view.locator('#join-room-body').waitFor({state: 'hidden'});
          if (width === 390) {
            await view.locator('.oie-back-button').click();
            await view.waitForURL(url => !url.searchParams.has('chat'));
          }
          await view.locator('[data-new-conversation]').click();
          await view.locator('#newchat-body [data-room-entry-open=create-room]').click();
          await view.locator('#create-room-form').waitFor();
          const createdIssues = await scan('#create-room-body');
          assert.deepEqual(createdIssues.violations.map(value => value.id), [], ['create', width, theme, mode].join(' '));
          await view.screenshot({path: path.join(artifacts, `room-create-${width}-${theme}-${mode}.png`), animations: 'disabled'});
          await view.close();
        }
      }
    }
    assert.deepEqual(errors, []);
    console.log('PASS: in-message vCard, stable fallback, keyboard invite, delayed scoped preview, draft and painted geometry, confirmed entry, creation and preview retry, eight responsive theme and mode combinations');
  } finally {
    release?.();
    await browser.close();
    child.kill('SIGTERM');
    for (const stream of streams) stream.end();
    api.closeAllConnections();
    await new Promise(resolve => api.close(resolve));
  }
})().catch(error => { console.error(error); process.exitCode = 1; });
