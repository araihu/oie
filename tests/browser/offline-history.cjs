const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const path = require('node:path');
const vm = require('node:vm');

function savedHistory() {
  const worker = navigator.serviceWorker.controller;
  if (!worker) return null;
  const {port1, port2} = new MessageChannel();
  return new Promise(resolve => {
    const finish = value => { clearTimeout(timeout); port1.close(); resolve(value); };
    const timeout = setTimeout(() => finish(null), 1000);
    port1.onmessage = ({data}) => finish(data);
    worker.postMessage({type: 'oie:offline-history-read'}, [port2]);
  });
}

async function checkNavigationAuthority(id) {
  const engine = id(1), other = id(2);
  const saved = {engine, checkpoint: engine + ':1', entries: [{cursor: engine + ':1'}]};
  const source = await fs.readFile(path.join(__dirname, '../../internal/adapters/web/offline-worker.js'), 'utf8');
  const handlers = new Map();
  const requests = new Map();
  const sandbox = vm.createContext({
    self: {addEventListener: (type, handler) => handlers.set(type, handler), location: {origin: 'http://test'}},
    URL, Response, TextEncoder, caches: {match: async () => null},
    fetch: request => new Promise((resolve, reject) => requests.set(request.key, {resolve, reject})),
  });
  vm.runInContext(source, sandbox);
  let current = saved;
  sandbox.state = async update => current = update?.(current) ?? current;
  const send = key => sandbox.workspace({key, signal: {aborted: false}}, new URL('http://test/'));
  const ok = identity => new Response('workspace', {headers: {
    'X-Oie-Offline-History': identity, 'X-Oie-Offline-Cursor': identity + ':1',
  }});
  const denied = () => new Response('denied', {status: 503, headers: {'X-Oie-Offline-History': 'denied'}});

  const earlierSuccess = send('old success');
  const laterDenial = send('recent denial');
  requests.get('recent denial').resolve(denied());
  await laterDenial;
  requests.get('old success').resolve(ok(engine));
  await earlierSuccess;
  assert(current.blocked && !current.entries.length, 'an earlier verification cannot reopen a denied scope');

  current = saved;
  const earlierDenial = send('known denial');
  const laterOutage = send('outage');
  requests.get('known denial').resolve(denied());
  await earlierDenial;
  requests.get('outage').reject(new TypeError('offline'));
  await laterOutage;
  assert(current.blocked && !current.entries.length, 'a newer network outage cannot conceal a confirmed denial');

  current = saved;
  current = sandbox.invalidate(current, {engine, chat: id(9), through: engine + ':3', confirmedMissing: true});
  assert.equal(current.checkpoint, engine + ':1', 'another chat’s 404 cannot certify an unobserved event gap');
  assert.equal(sandbox.progress(current, {engine, through: engine + ':4'}), undefined, 'an isolated event cannot certify an earlier gap');
  const afterGap = sandbox.verify(current, engine, engine + ':4');
  assert.equal(afterGap.entries.length, 0, 'the unobserved gap still invalidates older history');
  assert.equal(afterGap.checkpoint, engine + ':4');

  current = saved;
  let reset;
  handlers.get('fetch')({
    request: {key: 'reset', method: 'POST', url: 'http://test/ui/engine/reset'},
    respondWith: promise => { reset = promise; },
  });
  const replacement = send('new engine');
  requests.get('new engine').resolve(ok(other));
  await replacement;
  current = {...current, entries: [{cursor: other + ':1'}]};
  requests.get('reset').resolve(new Response(null, {status: 204, headers: {'X-Oie-Reset-Engine': engine}}));
  await reset;
  assert.equal(current.engine, other);
  assert.equal(current.entries.length, 1, 'the delayed result of the old reset preserves the new engine');
}

module.exports = async function checkOfflineHistory({browser, base, account, chat, state, until, id}) {
  await checkNavigationAuthority(id);
  const artifacts = path.join(__dirname, 'artifacts');
  await fs.mkdir(artifacts, {recursive: true});
  const latest = 'A mensagem chegou antes da queda. <b>Texto literal</b>';
  const draft = 'Meu rascunho ainda está aqui.';
  const withdrawn = 'Original retraído que não pode aparecer sem conexão.';
  const missing = id(999);
  const messages = [
    {id: id(101), revisionId: id(201), originalText: withdrawn, createdAt: '2026-11-07T19:00:00Z',
      retraction: {at: '2026-11-07T19:01:00Z', originalAvailable: true}},
    {id: id(102), revisionId: id(202), originalText: latest, createdAt: '2026-11-07T20:00:00Z'},
  ];
  state.messages = messages.map(message => ({
    ...message, conversationId: chat, accountId: account, sender: {id: id(6), displayName: 'Bob'},
    isOwn: false, sendState: 'received',
  }));

  for (const [width, lang, theme] of [[390, 'pt-BR', 'light'], [1440, 'en', 'dark']]) {
    const context = await browser.newContext({viewport: {width, height: 760}, locale: lang});
    const timeZone = 'Asia/Tokyo';
    await context.addInitScript(zone => {
      localStorage.setItem('oie.device.time-zone.v1', zone);
    }, timeZone);
    const errors = [];
    const newPage = async () => {
      const page = await context.newPage();
      page.on('pageerror', error => errors.push(error.message));
      return page;
    };
    const root = base + '/?' + new URLSearchParams({lang, theme});
    const route = root + '&chat=' + chat;
    const status = lang === 'pt-BR' ? 'Sem conexão' : 'Offline';
    try {
      let page = await newPage();
      await page.goto(route);
      await page.locator('#message-composer').fill(draft);
      await until(async () => (await page.evaluate(savedHistory))?.entries.some(entry =>
        entry.messages.some(message => message.text === latest)), 'message cached for offline use');

      // A new document can open the inbox and follow a saved chat with no network.
      await page.close();
      await context.setOffline(true);
      page = await newPage();
      await page.goto(root);
      await page.getByText(status, {exact: true}).waitFor();
      await page.locator('#offline-conversations').getByRole('link', {name: /Bob/}).click();
      const timeline = page.locator('#offline-messages');
      await timeline.getByText(latest, {exact: true}).waitFor();
      assert.equal(await timeline.locator('b').count(), 0, 'offline content is rendered as text');
      const expectedTime = await page.evaluate(({lang, timeZone, at}) =>
        new Intl.DateTimeFormat(lang, {dateStyle: 'short', timeStyle: 'short', timeZone}).format(new Date(at)),
        {lang, timeZone, at: messages[1].createdAt});
      assert.equal(await timeline.locator('time').last().textContent(), expectedTime, 'offline uses Oiê’s configured zone');
      assert.equal(await page.getByText(withdrawn, {exact: true}).count(), 0, 'withdrawn original is excluded');
      await timeline.getByText(lang === 'pt-BR' ? 'Mensagem retraída' : 'Message retracted', {exact: true}).waitFor();
      assert.equal(await page.locator('#message-composer').count(), 0);
      assert.equal(await timeline.getByText(draft, {exact: true}).count(), 0);
      assert.equal(await page.evaluate(() => document.documentElement.style.colorScheme), theme);
      assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth));
      await page.screenshot({path: path.join(artifacts, 'offline-history-' + lang + '.png')});

      await page.goto(root + '&chat=' + missing);
      await page.locator('#offline-empty').waitFor();
      assert.equal(await timeline.locator('li').count(), 0, 'a cache miss cannot show a different chat');
      await page.goto(route + '&account=' + missing);
      await page.locator('#offline-empty').waitFor();
      assert.equal(await timeline.locator('li').count(), 0, 'a different account cannot reuse a snapshot');
      await page.goto(route + '&notificationEngine=' + missing);
      await page.locator('#offline-empty').waitFor();
      assert.equal(await timeline.locator('li').count(), 0, 'a different engine cannot reuse a snapshot');

      await page.goto(route);
      await timeline.getByText(latest, {exact: true}).waitFor();
      await context.setOffline(false);
      await page.locator('#offline-retry').click();
      await until(async () => await page.locator('#message-composer').inputValue() === draft, 'draft after offline browsing');
      assert.equal(state.effects, 0, 'reading offline and reconnecting does not send a draft');

      // A reachable BFF can lose its connection to the engine independently.
      await page.goto(root + '&inbox=1');
      await page.locator('#conversation-list a[data-conversation-id="' + chat + '"]').waitFor({state: 'visible'});
      state.engineDown = true;
      await page.locator('#conversation-list a[data-conversation-id="' + chat + '"]').click();
      await timeline.getByText(latest, {exact: true}).waitFor();
      state.engineDown = false;
      await page.locator('#offline-retry').click();
      await page.locator('#chat-header h2').getByText('Bob', {exact: true}).waitFor();

      const stale = await page.evaluate(savedHistory);
      assert.equal(stale.entries.length, 1);
      await page.goto(root + '&inbox=1');
      state.chatRemoved = true;
      const missingFragment = page.waitForResponse(response =>
        new URL(response.url()).searchParams.get('chat') === chat &&
        response.request().headers()['hx-request'] === 'true' && response.status() === 404);
      await page.locator('#conversation-list a[data-conversation-id="' + chat + '"]').click();
      await missingFragment;
      await until(async () => (await page.evaluate(savedHistory))?.entries.length === 0, 'HTMX 404 evicts the chat');
      await page.evaluate(({engine, entries: [entry]}) => navigator.serviceWorker.controller.postMessage({
        type: 'oie:offline-history-save', engine, cursor: entry.cursor, entry,
      }), stale);
      await context.setOffline(true);
      await page.goto(route);
      await page.locator('#offline-empty').waitFor();
      assert.equal(await timeline.locator('li').count(), 0, 'a delayed snapshot cannot restore a confirmed missing chat');

      // A new online document can miss events while every tab was closed.
      state.chatRemoved = false;
      state.seq++;
      await context.setOffline(false);
      await page.goto(route);
      await until(async () => (await page.evaluate(savedHistory))?.entries.length === 1, 'chat cached again after a newer snapshot');
      await page.close();
      state.seq++;
      page = await newPage();
      await page.goto(root + '&inbox=1');
      await until(async () => (await page.evaluate(savedHistory))?.entries.length === 0, 'unobserved event gap invalidates the old snapshot');

      // Malformed authentication responses are distinct from engine downtime.
      await page.goto(route);
      await until(async () => (await page.evaluate(savedHistory))?.entries.length === 1, 'cached before denied response');
      state.engineDenied = true;
      const denied = await page.reload();
      assert.equal(denied.status(), 503);
      assert.equal(denied.headers()['x-oie-offline-history'], 'denied');
      await page.locator('#oie-route[data-engine-unavailable]').waitFor();
      await until(async () => (await page.evaluate(savedHistory))?.entries.length === 0, 'denied response purges history');
      assert.deepEqual(errors, []);
      console.log('PASS: Offline history, draft recovery and engine outage (' + lang + ', ' + width + ' px)');
    } finally {
      state.engineDown = false;
      state.chatRemoved = false;
      state.engineDenied = false;
      await context.close();
    }
  }
};
