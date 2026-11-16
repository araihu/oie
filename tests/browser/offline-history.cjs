const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const path = require('node:path');

module.exports = async function checkOfflineHistory({browser, base, account, chat, state, until, id}) {
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
      await page.waitForFunction(async expected => {
        const worker = navigator.serviceWorker.controller;
        if (!worker) return false;
        const channel = new MessageChannel();
        return new Promise(resolve => {
          channel.port1.onmessage = ({data}) => {
            channel.port1.close();
            resolve(data?.entries.some(entry => entry.messages.some(message => message.text === expected)));
          };
          worker.postMessage({type: 'oie:offline-history-read'}, [channel.port2]);
        });
      }, latest);

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

      state.chatRemoved = true;
      assert.equal((await page.goto(route)).status(), 404);
      await context.setOffline(true);
      await page.reload();
      await page.locator('#offline-empty').waitFor();
      assert.equal(await timeline.locator('li').count(), 0, 'a confirmed removal invalidates the saved chat');
      assert.deepEqual(errors, []);
      console.log('PASS: Offline history, draft recovery and engine outage (' + lang + ', ' + width + ' px)');
    } finally {
      state.engineDown = false;
      state.chatRemoved = false;
      await context.close();
    }
  }
};
