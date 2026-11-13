// Independent integration observer: drives real rendered forms and checks the
// real engine/Prosody result. It never substitutes a mocked send response.
const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const {chromium} = require('playwright');
const {schedulingFlow} = require('./scheduling-flow.cjs');

(async () => {
  const base = process.env.OIE_LIVE_URL || 'http://127.0.0.1:18087';
  const engine = process.env.OIE_ENGINE_URL || 'http://127.0.0.1:18086';
  const endpoint = process.env.OIE_XMPP_ENDPOINT || '127.0.0.1:5222';
  const prefix = process.env.OIE_BROWSER_SCENE || 'real-engine';
  const fixturePrefix = process.env.OIE_XMPP_BROWSER_FIXTURES === '1' ? 'browser-' : '';
  const aliceAddress = `${fixturePrefix}alice@localhost`;
  const bobAddress = `${fixturePrefix}bob@localhost`;
  const browser = await chromium.launch({headless: true});
  const evidence = {backend: 'real Oie engine + development Prosody', workflows: [], consoleErrors: [], expectedTransportFailures: []};
  const api = async path => {
    const response = await fetch(engine + path);
    assert.equal(response.status, 200, 'engine GET ' + path);
    return response.json();
  };
  const until = async (fn, name, timeout = 20000) => {
    const end = Date.now() + timeout;
    while (Date.now() < end) { if (await fn()) return; await new Promise(r => setTimeout(r, 50)); }
    throw Error('Timed out: ' + name);
  };
  const choose = async (page, form, label) => {
    await form.getByRole('combobox').click();
    await page.getByRole('option').filter({hasText: label}).click();
  };
  const openAccounts = async page => {
    await page.locator('[data-account-switcher]:visible').first().click();
    await page.locator('[data-open-accounts]:visible').first().click();
    await page.locator('#account-overview').waitFor({state: 'visible'});
  };
  const closeAccounts = async page => {
    await page.keyboard.press('Escape');
    await page.getByRole('dialog', {name: 'Accounts', exact: true}).waitFor({state:'hidden'});
  };
  const connectForm = (page, account) => page.locator('form[data-connect-account]').filter({has: page.locator('input[name="accountId"][value="' + account.id + '"]')});
  const disconnectForm = (page, account) => page.locator('form[data-disconnect-account]').filter({has: page.locator('input[name="accountId"][value="' + account.id + '"]')});
  const connect = async (page, account, password) => {
    if (!(await connectForm(page,account).isVisible())) {
      if (await page.locator('[data-account-back]:visible').count()) await page.locator('[data-account-back]:visible').click();
      await page.locator('[data-manage-account="'+account.id+'"]').click();
    }
    const form = connectForm(page, account);
    await form.waitFor({state:'visible'});
    assert.equal(await page.locator('[data-connect-account]').count(),1,'only one account form is rendered');
    if (!(await form.locator('[name="endpoint"]').isVisible())) await form.getByRole('button',{name:'Advanced connection settings',exact:true}).click();
    await form.locator('[name="password"]').fill(password);
    await form.locator('[name="endpoint"]').fill(endpoint);
    const [reply]=await Promise.all([page.waitForResponse(r=>r.url().endsWith('/ui/accounts/connect')&&r.request().method()==='POST'),form.locator('button[type="submit"]').click()]);
    assert.equal((await reply.json()).ok,true,'connection configuration accepted by real BFF');
    await until(async () => {const connection=await api('/api/v1/accounts/' + account.id + '/connection');if(connection.state==='error')throw Error('XMPP connection failed: '+connection.lastErrorCode);return connection.state==='connected'}, 'verified XMPP connection ' + account.address);
    await until(async () => await page.locator('.oie-account-detail [data-account-status="' + account.id + '"][data-connection-state="connected"]').count() > 0, 'visible connected avatar state');
    const remainingPassword = form.locator('[name="password"]');
    if (await remainingPassword.count()) assert.equal(await remainingPassword.inputValue(), '', 'password must clear after accepted connection');
  };
  const send = async (page, text) => {
    const form = page.locator('#send-message-form');
    await form.locator('#message-composer').fill(text);
    const operation=await form.locator('[name="operationKey"]').inputValue();
    await form.locator('[data-send-message]').click();
    await until(async () => (await form.locator('#message-composer').getAttribute('aria-readonly')) === 'false' && (await form.locator('[name="operationKey"]').inputValue()) !== operation && (await form.getAttribute('data-busy')) !== 'true', 'engine acceptance settles the pending journal and unlocks a fresh operation');
    assert.equal((await api('/api/v1/operations/'+operation)).kind,'send_message','real engine stores the accepted send receipt');
    assert.equal(await form.locator('#message-composer').inputValue(),'','composer is empty after engine acceptance');
  };
  const pickAccount = async (page, label, {empty = false} = {}) => {
    await page.locator('[data-account-switcher]:visible').first().click();
    const choice=page.locator('[data-switch-account]:visible').filter({hasText:label});
    const account=await choice.getAttribute('data-switch-account');
    const generation=await page.evaluate(()=>window.OieRuntime.generation);
    await choice.click();
    await page.waitForURL(url => (url.searchParams.get('account') || '') === account);
    assert.equal(await page.locator('[data-account-switcher]:visible').textContent().then(t=>t.includes(label)),true);
    const conversationsBefore = await api('/api/v1/conversations?accountId=' + account);
    const expectedEngine = (await api('/api/v1/engine')).id;
    assert(conversationsBefore.items.every(item => item.accountId === account), 'engine conversation list is account-scoped');
    const emptyInbox = conversationsBefore.items.length === 0;
    if(empty) assert(emptyInbox, 'the newly connected account has an empty inbox');
    try {
      await until(async()=>page.evaluate(({expectedAccount,expectedEngine,generation,emptyInbox})=>{
        if(document.readyState!=='complete'||window.OieRuntime.generation<=generation)return false;
        const pane=document.getElementById('conversation-pane');
        if(!pane||document.body.dataset.engineId!==expectedEngine||pane.dataset.engineId!==expectedEngine||pane.dataset.conversationId!==document.body.dataset.conversationId)return false;
        const input=document.querySelector('#message-composer');
        if(emptyInbox)return !input&&!document.body.dataset.accountId&&!document.body.dataset.conversationId&&!pane.dataset.accountId&&pane.dataset.openChat==='false';
        if(document.body.dataset.accountId!==expectedAccount||pane.dataset.accountId!==expectedAccount||!pane.dataset.conversationId||!input)return false;
        const key=['oie.draft.v1',document.body.dataset.engineId,expectedAccount,document.body.dataset.conversationId].join(':');
        const raw=localStorage.getItem(key),saved=raw?JSON.parse(raw):null;
        return input.value===(saved?.text||'');
      },{expectedAccount:account,expectedEngine,generation,emptyInbox}),'initialized account-scoped inbox or device draft for '+label);
      if(emptyInbox) {
        assert.deepEqual((await api('/api/v1/conversations?accountId='+account)).items, [], 'selecting a fresh account does not create a conversation');
        evidence.workflows.push('Select an account with a verified empty engine inbox: settled matching pane, no selected conversation, composer or cross-account draft');
      }
    } catch(error) {
      const state=await page.evaluate(()=>({url:location.href,ready:document.readyState,body:{...document.body.dataset},pane:document.getElementById('conversation-pane')?{...document.getElementById('conversation-pane').dataset}:null,composer:!!document.querySelector('#message-composer'),generation:window.OieRuntime.generation}));
      const conversations=await api('/api/v1/conversations?accountId='+account);
      await fs.writeFile('artifacts/'+prefix+'-account-draft-timeout.json',JSON.stringify({label,expectedAccount:account,previousGeneration:generation,state,conversations},null,2));
      await page.screenshot({path:'artifacts/'+prefix+'-account-draft-timeout.png',fullPage:true});
      throw error;
    }

  };
  try {
    await fs.mkdir('artifacts', {recursive: true});
    const aliceContext = await browser.newContext({viewport: {width: 1440, height: 900}, locale: 'en-US', timezoneId: 'America/Sao_Paulo'});
    const bobContext = await browser.newContext({viewport: {width: 1440, height: 900}, locale: 'en-US', timezoneId: 'America/Sao_Paulo'});
    const alicePage = await aliceContext.newPage();
    const bobPage = await bobContext.newPage();
    const preferenceConflictURLs=new Map(),consoleLocations=[];
    const searchTransport=[];alicePage.on('response',async response=>{if(response.url().includes('/ui/message-search?'))searchTransport.push({url:response.url(),status:response.status(),body:await response.text().catch(()=>'<response unavailable>')})});
    let expectedLostReply = false;
    for (const page of [alicePage, bobPage]) {
      page.on('pageerror', error => evidence.consoleErrors.push(error.message));
      page.on('console', message => {if (message.type() === 'error') {
        if (expectedLostReply && message.text().includes('net::ERR_FAILED')) evidence.expectedTransportFailures.push(message.text());
        else {evidence.consoleErrors.push(message.text());consoleLocations.push(message.location().url||'')}
      }});
    }
    await alicePage.goto(base + '/?theme=dark&lang=en');
    await openAccounts(alicePage);
    const accounts = [];
    for (const [address, label] of [[aliceAddress, 'Alice development'], [bobAddress, 'Bob development']]) {
      if (await alicePage.locator('[data-account-back]:visible').count()) await alicePage.locator('[data-account-back]:visible').click();
      await alicePage.locator('[data-account-add]').click();
      const form = alicePage.locator('#register-account-form');
      await form.locator('[name="address"]').fill(address);
      await form.locator('[name="label"]').fill(label);
      await form.locator('button[type="submit"]').click();
      await until(async () => (await api('/api/v1/accounts')).items.some(a => a.address === address), 'account created by browser ' + address);
      const saved = (await api('/api/v1/accounts')).items.find(a => a.address === address);
      accounts.push(saved);
      await alicePage.waitForURL(u=>u.searchParams.get('managedAccount')===saved.id);
      await connectForm(alicePage,saved).waitFor({state:'visible'});
    }
    const [alice, bob] = accounts;
    await connect(alicePage, alice, 'alice-local-only');
    await connect(alicePage, bob, 'bob-local-only');
    evidence.workflows.push('Register and connect both development accounts through rendered forms; actual TLS states and cleared passwords');
    await closeAccounts(alicePage);
    await pickAccount(alicePage, 'Alice development', {empty: true});
    await alicePage.locator('[data-new-conversation]:visible').first().click();
    const newChat = alicePage.locator('#new-conversation-form');
    await newChat.waitFor({state: 'visible'});
    await choose(alicePage, newChat, 'Alice development');
    await newChat.locator('[name="address"]').fill(bobAddress);
    await newChat.locator('button[type="submit"]').click();
    await alicePage.locator('#message-composer').waitFor({state: 'visible'});
    const nonce = Date.now().toString(36);
    const firstText = 'Olá, Bob! 👋\nA real XMPP message <&> ' + nonce;
    let aliceConversation;
    await until(async () => {
      const list = await api('/api/v1/conversations?accountId=' + alice.id);
      aliceConversation = list.items.find(c => c.address === bobAddress); return !!aliceConversation;
    }, 'Alice conversation created');
    await bobPage.goto(base + '/?theme=dark&lang=en&account=' + bob.id);
    const originalOperation = await alicePage.locator('#send-message-form [name="operationKey"]').inputValue();
    let replyLost = false, acceptedMessageId;
    const loseReply = async route => {
      const response = await route.fetch();
      assert.equal(response.status(),200);
      const result = await response.json();
      assert(result.ok && result.accepted, 'injected loss occurs only AFTER real engine acceptance');
      acceptedMessageId = result.messageId;
      await route.abort('failed');
      replyLost = true;
    };
    expectedLostReply = true;
    // Keep automatic reconciliation unavailable until the explicit retry.
    const loseReconciliation = route => route.abort('failed');
    await alicePage.route('**/ui/messages/reconcile',loseReconciliation);
    await alicePage.route('**/ui/messages/send',loseReply);
    await alicePage.locator('#message-composer').fill(firstText);
    await alicePage.locator('[data-send-message]').click();
    await until(async () => alicePage.locator('#message-composer').getAttribute('aria-readonly').then(value=>value==='true'), 'uncertain submission locks original body');
    await until(async () => replyLost, 'discarded the real successful browser response');
    await until(async () => (await api('/api/v1/operations/' + originalOperation)).kind === 'send_message', 'real acceptance receipt despite lost browser response');
    const uncertain = alicePage.locator('[data-pending-send][data-pending-state="uncertain"]');
    await uncertain.waitFor({state:'visible'});
    assert.equal(await uncertain.locator('[data-pending-send-text]').textContent(),firstText, 'pending bubble retains the immutable original body');
    assert.equal(await alicePage.locator('#message-composer').inputValue(),'','pending body moves out of the composer');
    assert.equal(await alicePage.locator('[data-send-message]').isDisabled(),true,'a pending send cannot be resubmitted through the composer');
    assert.equal(await alicePage.locator('#send-message-form [name="operationKey"]').inputValue(),originalOperation);
    const retained = await alicePage.evaluate(() => {
      const key=['oie.draft.v1',document.body.dataset.engineId,document.body.dataset.accountId,document.body.dataset.conversationId].join(':');
      return JSON.parse(localStorage.getItem(key));
    });
    assert.equal(retained.text,firstText,'device journal retains the lost-response body');
    assert.equal(retained.key,originalOperation,'device journal retains the original operation key');
    assert.equal(retained.pending,true,'uncertain submission remains pending');
    await until(async () => (await alicePage.locator('#send-message-form').getAttribute('data-busy')) !== 'true', 'lost response shows retry state');
    await alicePage.unroute('**/ui/messages/send',loseReply);
    const [retryResponse] = await Promise.all([
      alicePage.waitForResponse(r=>r.url().endsWith('/ui/messages/send')&&r.request().method()==='POST'),
      uncertain.locator('[data-pending-send-retry]').click(),
    ]);
    const retryBody=new URLSearchParams(retryResponse.request().postData());
    assert.equal(retryBody.get('operationKey'),originalOperation,'explicit retry reuses the immutable operation key');
    assert.equal(retryBody.get('originalText'),firstText,'explicit retry reuses the immutable body');
    const retryResult=await retryResponse.json();
    assert(retryResult.ok&&retryResult.accepted,'real engine accepts the explicit retry');
    assert.equal(retryResult.messageId,acceptedMessageId,'retry returns the original canonical message');
    await alicePage.unroute('**/ui/messages/reconcile',loseReconciliation);
    await until(async () => (await alicePage.locator('#message-composer').getAttribute('aria-readonly')) === 'false' && (await alicePage.locator('#send-message-form [name="operationKey"]').inputValue()) !== originalOperation, 'same-key retry reconciles accepted send and unlocks a fresh operation');
    assert.equal(await alicePage.locator('#message-composer').inputValue(),'');
    expectedLostReply = false;
    evidence.workflows.push('Drop the actual successful browser response, retain locked body/key, reconcile/retry once without a second engine/provider effect');
    let bobConversation;
    await until(async () => {
      const list = await api('/api/v1/conversations?accountId=' + bob.id);
      bobConversation = list.items.find(c => c.address === aliceAddress); return !!bobConversation;
    }, 'Bob conversation created by incoming XMPP');
    await bobPage.locator('a[data-conversation-id="' + bobConversation.id + '"]').waitFor({state: 'visible'});
    await bobPage.locator('a[data-conversation-id="' + bobConversation.id + '"]').click();
    const request = bobPage.locator('[data-conversation-request-panel]');
    const archive = request.getByRole('button', {name:'Archive', exact:true});
    assert(await request.evaluate(el=>el.classList.contains('oie-request-banner')&&el.getAttribute('role')==='region'), 'the request owns the banner and actions');
    assert(await request.evaluate(el=>getComputedStyle(el.parentElement).backgroundImage.includes('/ui/chat-pattern.svg')), 'the request uses the chat wallpaper');
    await request.getByRole('heading', {name:'Message request'}).waitFor();
    const accept = request.getByRole('button', {name:'Accept', exact:true});
    await accept.waitFor();
    assert(await archive.evaluate(button=>button.classList.contains('bg-warning-action')), 'archive has the warning tone');
    assert.match(await archive.locator('use').getAttribute('href'), /#hi-16-solid-folder$/);
    assert.match(await accept.locator('use').getAttribute('href'), /#hi-16-solid-check$/);
    assert.equal(await bobPage.locator('#message-timeline, #message-composer').count(),0);
    assert(!(await bobPage.content()).includes(firstText), 'pending message is absent from rendered HTML');
    assert.equal((await api('/api/v1/conversations/'+bobConversation.id+'/messages')).items.length,0);
    let completeArchive;
    const archiveDelay = new Promise(resolve => {completeArchive=resolve});
    await bobPage.route('**/ui/conversations/preferences', async route => {
      await archiveDelay;
      await route.continue();
    }, {times:1});
    const beforeArchive = await archive.boundingBox();
    await archive.click();
    const frameGeometry = await archive.evaluate(button => new Promise(resolve => {
      const positions = [];
      const frame = () => {
        const {x,y,width,height} = button.getBoundingClientRect();
        positions.push({x,y,width,height});
        if (positions.length===8) resolve(positions); else requestAnimationFrame(frame);
      };
      requestAnimationFrame(frame);
    }));
    for (const rect of frameGeometry) for (const axis of ['x','y','width','height']) {
      assert(Math.abs(rect[axis]-beforeArchive[axis])<=1, `request actions stay in place while archiving (${axis})`);
    }
    assert.match(await archive.locator('use').getAttribute('href'), /#hi-16-solid-folder$/);
    completeArchive();
    await until(async () => (await api('/api/v1/conversations/'+bobConversation.id)).archived, 'request archived');
    await bobPage.locator('a[data-conversation-id="' + bobConversation.id + '"]').waitFor({state:'hidden'});
    await bobPage.setViewportSize({width:390,height:844});
    await bobPage.goto(base+'/?section=archived&chat='+bobConversation.id+'&account='+bob.id+'&theme=dark&lang=pt-BR');
    await request.getByRole('heading', {name:'Solicitação de mensagem'}).waitFor();
    assert(await bobPage.evaluate(()=>document.documentElement.scrollWidth<=innerWidth), 'request fits the mobile viewport');
    await bobPage.screenshot({path:'artifacts/'+prefix+'-request-mobile-pt.png'});
    const mobileAccept = request.getByRole('button', {name:'Aceitar', exact:true});
    const beforeRetry = [await request.boundingBox(), await mobileAccept.boundingBox()];
    let firstAcceptOperation;
    await bobPage.route('**/ui/conversations/preferences', route => {
      firstAcceptOperation = new URLSearchParams(route.request().postData()).get('operationKey');
      return route.fulfill({json: {}});
    }, {times:1});
    await mobileAccept.click();
    await request.getByRole('status').filter({hasText:'Não foi possível confirmar a alteração'}).waitFor();
    assert.equal(await mobileAccept.getAttribute('aria-describedby'), 'conversation-preference-status');
    assert.deepEqual([await request.boundingBox(), await mobileAccept.boundingBox()], beforeRetry, 'the retry keeps the mobile banner and action in place');
    const [accepted] = await Promise.all([
      bobPage.waitForResponse(response => response.url().endsWith('/ui/conversations/preferences') && response.request().method() === 'POST'),
      mobileAccept.click(),
    ]);
    assert(firstAcceptOperation);
    assert.equal(new URLSearchParams(accepted.request().postData()).get('operationKey'), firstAcceptOperation);
    await request.waitFor({state:'detached'});
    await bobPage.setViewportSize({width:1440,height:900});
    await bobPage.goto(base+'/?chat='+bobConversation.id+'&account='+bob.id+'&theme=dark&lang=en');
    evidence.workflows.push('First direct message is hidden until accepted, including when archived and recovered');
    await bobPage.locator('[data-message-id]').filter({hasText: firstText}).waitFor({state: 'visible'});
    let bobMessages = await api('/api/v1/conversations/' + bobConversation.id + '/messages');
    assert.equal(bobMessages.items.filter(m => m.originalText === firstText && !m.isOwn).length, 1, 'one actual recipient effect');
    evidence.workflows.push('Start an account-scoped conversation and receive a Unicode/multiline/XML-character message over real XMPP');
    const draft = 'Keep this unsent draft while Bob replies';
    await alicePage.locator('#message-composer').fill(draft);
    const replyText = 'Hi Alice, received! ' + nonce;
    await send(bobPage, replyText);
    await alicePage.locator('[data-message-id]').filter({hasText: replyText}).waitFor({state: 'visible'});
    assert.equal(await alicePage.locator('#message-composer').inputValue(), draft, 'live update preserved unsent draft');
    await pickAccount(alicePage,'Bob development');
    assert.equal(await alicePage.locator('#message-composer').inputValue(),'','Alice draft must not move to Bob');
    await pickAccount(alicePage,'Alice development');
    // Archive recovery can make another conversation the account's latest.
    await alicePage.locator('a[data-conversation-id="'+aliceConversation.id+'"]').click();
    await until(async()=>alicePage.evaluate(({conversation,draft})=>document.body.dataset.conversationId===conversation&&document.querySelector('#message-composer')?.value===draft,{conversation:aliceConversation.id,draft}),'the original conversation restores its device draft');
    assert.equal(await alicePage.locator('#message-composer').inputValue(),draft,'switching back restores the account-scoped draft');
    await alicePage.locator('#message-composer').fill('');
    evidence.workflows.push('Bob replies; Alice receives automatically without losing her unsent draft; profile switching keeps drafts isolated');
    const ownStored = (await api('/api/v1/conversations/' + aliceConversation.id + '/messages')).items.find(m => m.originalText === firstText && m.isOwn);
    const ownTime = alicePage.locator('[data-message-id="' + ownStored.id + '"] time[datetime]').last();
    await until(async () => ownTime.evaluate(time => {
      const hour = new Date(time.dateTime).getHours();
      const visibleHour = Number(time.textContent.match(/\d+/)?.[0]);
      return visibleHour === hour || visibleHour === (hour % 12 || 12);
    }), 'timestamps follow browser device timezone');

    // Exercise app-owned star metadata through the real BFF/API/store. A second
    // UI observes the same source, while Bob's account-scoped copy stays separate.
    const starPeer = await aliceContext.newPage();
    try {
      await starPeer.goto(base + '/?chat=' + aliceConversation.id + '&account=' + alice.id + '&lang=en&theme=dark');
      await starPeer.locator('#message-composer').waitFor({state:'visible'});
      const starDraft = 'Preserve this draft while starring a retained message';
      await alicePage.locator('#message-composer').fill(starDraft);
      const sourceRow = alicePage.locator('[data-message-id="' + ownStored.id + '"]');
      await sourceRow.hover();
      await sourceRow.locator('[data-message-menu-trigger]').click();
      const starAction = alicePage.locator('#star-message-' + ownStored.id);
      await starAction.waitFor({state:'visible'});
      assert.equal(await starAction.getAttribute('data-starred'),'false');
      await starAction.click();
      await until(async () => (await api('/api/v1/messages/' + ownStored.id + '/star')).starred, 'real source message star commits');
      await until(async () => sourceRow.getAttribute('data-starred').then(value => value === 'true'), 'the first tab displays the committed star');
      const recipientCopy = bobMessages.items.find(m => m.originalText === firstText && !m.isOwn);
      assert.equal((await api('/api/v1/messages/' + recipientCopy.id + '/star')).starred, false, 'a star does not leak into another account copy');
      const peerRow = starPeer.locator('[data-message-id="' + ownStored.id + '"]');
      await peerRow.hover();
      await peerRow.locator('[data-message-menu-trigger]').click();
      const peerAction = starPeer.locator('#star-message-' + ownStored.id);
      await until(async () => (await peerAction.getAttribute('data-starred')) === 'true', 'another tab observes star through real SSE');
      await alicePage.locator('#message-composer').focus();
      await peerAction.click();
      await until(async () => !(await api('/api/v1/messages/' + ownStored.id + '/star')).starred, 'another tab unstars the same source');
      await until(async () => sourceRow.getAttribute('data-starred').then(value => value === 'false'), 'the first tab reconciles the later unstar');
      assert.equal(await alicePage.locator('#message-composer').inputValue(), starDraft);
      assert(await alicePage.locator('#message-composer').evaluate(el => document.activeElement === el), 'background star refresh preserves composer focus');
      const sourceNow = await api('/api/v1/messages/' + ownStored.id);
      assert.equal(sourceNow.revisionId, ownStored.revisionId, 'star metadata does not edit message content');
      const recipientNow = await api('/api/v1/conversations/' + bobConversation.id + '/messages');
      assert.equal(recipientNow.items.filter(m => m.originalText === firstText && !m.isOwn).length, 1, 'starring causes no additional recipient effect');
      await alicePage.locator('#message-composer').fill('');
      evidence.workflows.push('Real source star/unstar through message menus, two-tab SSE, independent content revisions, account isolation and preserved draft/focus with no new protocol send');
    } finally { await starPeer.close(); }

    await alicePage.locator('a[href*="section=media"]:visible').first().click();
    await alicePage.locator('#shared-content-scroll').waitFor({state:'visible'});
    assert.equal(await alicePage.locator('#message-timeline').count(),0,'Alice has left the conversation before Bob sends the unread message');
    const unreadText='Unread while Alice is away '+nonce;
    await send(bobPage,unreadText);
    let unreadState;
    await until(async () => {
      unreadState=await api('/api/v1/conversations/'+aliceConversation.id+'/read-state');
      return unreadState.unreadCount>0;
    },'real incoming XMPP message remains unread while Alice browses shared content');
    await alicePage.locator('[data-account-switcher]:visible').click();
    const menu=alicePage.getByRole('dialog',{name:'Switch account',exact:true});
    const accountUnread=async ()=>(await api('/api/v1/conversations?accountId='+alice.id)).items.reduce((total,conversation)=>total+conversation.unreadCount,0);
    await until(async () => Number(await menu.locator('[data-account-unread="'+alice.id+'"]').textContent())===await accountUnread(),'profile unread badge agrees with aggregate account read state');
    const unread=Number(await menu.locator('[data-account-unread="'+alice.id+'"]').textContent());
    assert(unread>0,'profile shows actual unread messages for the account');
    assert.equal(unread,await accountUnread(),'rendered account unread count matches all retained conversations');
    await menu.locator('form[data-oie-action="disconnect"] button[type="submit"]').click();
    await until(async () => (await api('/api/v1/accounts/' + alice.id + '/connection')).state === 'not_connected', 'explicit profile disconnect');
    assert.equal((await api('/api/v1/accounts/'+bob.id+'/connection')).state,'connected','disconnect targets only the selected account');
    await alicePage.keyboard.press('Escape');await menu.waitFor({state:'hidden'});
    await alicePage.goto(base+'/?chat='+aliceConversation.id+'&account='+alice.id+'&lang=en&theme=dark');
    await alicePage.locator('#message-composer').waitFor({state:'visible'});
    const cancelledText = 'Cancel this queued message ' + nonce;
    await send(alicePage, cancelledText);
    let cancelled;
    await until(async () => {
      const messages = await api('/api/v1/conversations/' + aliceConversation.id + '/messages');
      cancelled = messages.items.find(m => m.originalText === cancelledText); return cancelled && cancelled.sendState === 'queued';
    }, 'queued message persisted while disconnected');
    const queuedRow = alicePage.locator('[data-message-id="' + cancelled.id + '"]');
    await queuedRow.locator('[data-message-menu-trigger]').click();
    await queuedRow.locator('[id^="cancel-message-"]').click();
    await until(async () => (await api('/api/v1/messages/' + cancelled.id)).sendState === 'cancelled', 'guarded queued cancellation');
    const queuedText = 'Send this as soon as we reconnect ' + nonce;
    await send(alicePage, queuedText);
    await openAccounts(alicePage);
    await connect(alicePage, alice, 'alice-local-only');
    await closeAccounts(alicePage);
    await bobPage.locator('[data-message-id]').filter({hasText: queuedText}).waitFor({state: 'visible'});
    bobMessages = await api('/api/v1/conversations/' + bobConversation.id + '/messages');
    assert.equal(bobMessages.items.filter(m => m.originalText === queuedText).length, 1);
    assert.equal(bobMessages.items.filter(m => m.originalText === cancelledText).length, 0, 'cancelled intent never reached recipient');
    evidence.workflows.push('Disconnect queues sends; queued cancellation is terminal; reconnect transmits eligible work once');
    const longText = Array.from({length: 95}, (_, i) => 'Earlier line ' + (i + 1) + ' for live scroll preservation.').join('\n') + '\n' + nonce;
    await send(alicePage,longText);
    await bobPage.locator('[data-message-id]').filter({hasText: longText}).waitFor({state:'attached'});
    const timeline=bobPage.locator('#message-timeline');
    await until(async()=>timeline.evaluate(el=>el.scrollHeight>el.clientHeight+500),'long real message gives a scrollable timeline');
    await timeline.evaluate(el=>{el.scrollTop=el.scrollHeight-el.clientHeight-400;el.dispatchEvent(new Event('scroll'));});
    const readingRow=bobPage.locator('[data-message-id]').filter({hasText:longText});
    const readingAnchor=await readingRow.evaluate(row=>({id:row.dataset.messageId,top:row.getBoundingClientRect().top-document.getElementById('message-timeline').getBoundingClientRect().top}));
    const whileReading='A new message while you read earlier history '+nonce;
    await send(alicePage,whileReading);
    await until(async () => (await api('/api/v1/conversations/'+bobConversation.id+'/messages')).items.some(message=>message.originalText===whileReading&&!message.isOwn),'the real recipient receives while Bob reads earlier history');
    await until(async () => (await bobPage.locator('a[data-conversation-id="'+bobConversation.id+'"]').textContent()).includes(whileReading),'live inbox observes the new message while the timeline may retain an older window');
    const anchorRow=timeline.locator('[data-message-id="'+readingAnchor.id+'"]');
    assert(await anchorRow.count(),'the reading anchor stays mounted');
    const anchorTop=await anchorRow.evaluate(row=>row.getBoundingClientRect().top-document.getElementById('message-timeline').getBoundingClientRect().top);
    assert(Math.abs(anchorTop-readingAnchor.top)<3,'incoming message preserves the visible reading anchor');
    await bobPage.locator('[data-jump-latest]').waitFor({state:'visible'});
    await bobPage.locator('[data-jump-latest]').click();
    await bobPage.locator('[data-message-id]').filter({hasText:whileReading}).waitFor({state:'attached'});
    await until(async()=>timeline.evaluate(el=>el.scrollHeight-el.scrollTop-el.clientHeight<80),'jump to latest loads the current end and follows it');
    evidence.workflows.push('Real incoming message preserves older scroll position and exposes a working jump-to-latest action');

    // Exercise the preference wiring through the real rendered menu, BFF,
    // HTTP contract and SQLite, independently of the isolated HTTP fixture.
    const preferenceDraft='Keep this draft while this conversation is archived '+nonce;
    await alicePage.locator('#message-composer').fill(preferenceDraft);
    await alicePage.evaluate(()=>{window.preferenceComposer=document.getElementById('message-composer');window.preferenceTimeline=document.getElementById('message-timeline');window.preferencePane=document.getElementById('conversation-pane')});
    const retainedBefore=await api('/api/v1/conversations/'+aliceConversation.id+'/messages?limit=100');
    const recipientBeforePreferences=await api('/api/v1/conversations/'+bobConversation.id+'/messages?limit=100');
    const historyBeforePreferences=await Promise.all([alice,bob].map(account=>api('/api/v1/accounts/'+account.id+'/history')));
    const connectionsBeforePreferences=await Promise.all([alice,bob].map(account=>api('/api/v1/accounts/'+account.id+'/connection')));
    const preference=async(field,value)=>{
      const activate=async()=>{
        await until(async()=>{
          const current=await api('/api/v1/conversations/'+aliceConversation.id);
          return await alicePage.locator('#chat-header [data-conversation-preference-state]').getAttribute('data-revision')===current.revisionId;
        },'current conversation revision before '+field);
        const prefix=field==='archived'?(value?'archive':'unarchive'):(value?'favorite':'unfavorite');
        const form=alicePage.locator('#conversation-preferences-'+aliceConversation.id+'-'+(field==='archived'?'archive':'favorite'));
        const operation=await form.locator('[name="operationKey"]').inputValue();
        await alicePage.locator('#chat-options button').first().click();
        const reply=alicePage.waitForResponse(response=>response.url().includes('/ui/conversations/preferences')&&response.request().method()==='POST');
        await alicePage.locator('#'+prefix+'-conversation-'+aliceConversation.id+':visible').click();
        const response=await reply;return {result:await response.json(),status:response.status(),url:response.url(),operation,form};
      };
      let attempt=await activate();
      if(!attempt.result.ok){
        assert.equal(attempt.result.code,'state_conflict','a concurrent incoming/read revision is an explicit CAS rejection');
        assert.equal(attempt.result.rejected,true);
        assert.equal(attempt.status,409,'the reviewed conflict is the actual HTTP rejection');
        preferenceConflictURLs.set(attempt.url,(preferenceConflictURLs.get(attempt.url)||0)+1);
        assert.equal(attempt.result.conversation.id,aliceConversation.id);
        assert.notEqual(attempt.result.conversation[field],value,'rejected command leaves the requested flag unchanged');
        assert.notEqual(attempt.result.nextKey,attempt.operation,'rejected command prepares a fresh intent');
        await until(async()=>{const fresh=await attempt.form.locator('[name="operationKey"]').inputValue(),current=await api('/api/v1/conversations/'+aliceConversation.id);return /^[0-9a-f-]{36}$/.test(fresh)&&fresh!==attempt.operation&&await attempt.form.locator('[name="expectedRevisionId"]').inputValue()===current.revisionId},'UI exposes a fresh key and current revision for the reviewed preference');
        attempt=await activate();
        evidence.workflows.push('A real concurrent preference revision conflict is surfaced and recovered through one reviewed fresh user action');
      }
      assert.equal(attempt.result.ok,true,'real preference accepted '+field+'='+value+' '+JSON.stringify(attempt.result));
      await until(async()=>{
        const current=await api('/api/v1/conversations/'+aliceConversation.id);
        return current[field]===value&&await alicePage.locator('#chat-header [data-conversation-preference-state]').getAttribute('data-'+field)===String(value);
      },'real retained '+field+'='+value);
    };
    await preference('favorite',true);
    await preference('archived',true);
    await until(async()=>await alicePage.locator('#conversation-list a[data-conversation-id="'+aliceConversation.id+'"]').count()===0,'archived source leaves the ordinary real inbox');
    assert(await alicePage.evaluate(()=>window.preferenceComposer===document.getElementById('message-composer')&&window.preferenceTimeline===document.getElementById('message-timeline')&&window.preferencePane===document.getElementById('conversation-pane')),'archiving preserves the selected conversation workspace');
    assert.equal(await alicePage.locator('#message-composer').inputValue(),preferenceDraft);
    assert.deepEqual(await api('/api/v1/conversations/'+aliceConversation.id+'/messages?limit=100'),retainedBefore,'preferences do not create or change source messages');
    assert.deepEqual(await api('/api/v1/conversations/'+bobConversation.id+'/messages?limit=100'),recipientBeforePreferences,'preferences do not produce a recipient protocol effect');
    for(const [index,account] of [alice,bob].entries()){
      assert.equal((await api('/api/v1/accounts/'+account.id+'/history')).retainedMessages,historyBeforePreferences[index].retainedMessages,'preferences preserve account history');
      assert.equal((await api('/api/v1/accounts/'+account.id+'/connection')).state,connectionsBeforePreferences[index].state,'preferences do not change protocol connection state');
    }
    const archivedIncoming='A real incoming message while the conversation is archived '+nonce;
    await send(bobPage,archivedIncoming);
    await until(async()=>(await api('/api/v1/conversations/'+aliceConversation.id+'/messages')).items.some(message=>message.originalText===archivedIncoming&&!message.isOwn),'actual XMPP arrival in an archived conversation');
    const stillArchived=await api('/api/v1/conversations/'+aliceConversation.id);
    assert(stillArchived.archived&&stillArchived.favorite,'new protocol traffic preserves both app-owned preferences');
    assert.equal(await alicePage.locator('#message-composer').inputValue(),preferenceDraft);
    await preference('archived',false);
    await preference('favorite',false);
    await until(async()=>await alicePage.locator('#conversation-list a[data-conversation-id="'+aliceConversation.id+'"]').count()===1,'unarchive restores the real inbox row');
    await alicePage.locator('#message-composer').fill('');
    evidence.workflows.push('Real menu archive/favorite writes persist through BFF/API/SQLite without message or connection effects, retain the selected workspace/draft, and stay archived on a new XMPP arrival before explicit reset');

    // Search follows the retained engine projection, then navigates back to the
    // canonical source through the existing bounded timeline contract.
    const searchDraft='Keep this draft while searching retained messages '+nonce;
    await alicePage.locator('#message-composer').fill(searchDraft);
    const searchSource=(await api('/api/v1/conversations/'+aliceConversation.id+'/messages?limit=100')).items.find(message=>message.originalText===archivedIncoming&&!message.isOwn);
    assert(searchSource,'search source is a real received XMPP message');
    const searchMessagesBefore=await Promise.all([aliceConversation,bobConversation].map(conversation=>api('/api/v1/conversations/'+conversation.id+'/messages?limit=100')));
    const searchConnectionsBefore=await Promise.all([alice,bob].map(account=>api('/api/v1/accounts/'+account.id+'/connection')));
    await alicePage.evaluate(()=>{window.searchComposer=document.getElementById('message-composer');window.searchTimeline=document.getElementById('message-timeline');window.searchDocument=Math.random()});
    const searchDocument=await alicePage.evaluate(()=>window.searchDocument);
    await alicePage.locator('#conversation-search').fill(archivedIncoming);
    const searchHit=alicePage.locator('[data-message-search-hit][data-search-message="'+searchSource.id+'"]').filter({has:alicePage.locator('[data-message-search-source]')});
    await searchHit.waitFor({state:'visible'}).catch(async error=>{console.error('Real search failure',JSON.stringify({transport:searchTransport,api:await api('/api/v1/messages/search?accountId='+alice.id+'&q='+encodeURIComponent(archivedIncoming)),ui:await alicePage.evaluate(()=>({url:location.href,query:document.getElementById('conversation-search')?.value,feedback:document.getElementById('message-search-feedback')?.textContent,root:document.querySelector('[data-message-search-root]')?.dataset,results:document.getElementById('message-search-results')?.innerText}))}));throw error});
    assert.equal(await searchHit.getAttribute('data-search-historical'),'false');
    const retainedSearch=await api('/api/v1/messages/search?accountId='+alice.id+'&q='+encodeURIComponent(archivedIncoming));
    assert.equal(retainedSearch.items.length,1,'exact current text has one canonical result in its owning account');
    assert.equal(retainedSearch.items[0].message.id,searchSource.id);
    assert(await alicePage.evaluate(()=>window.searchComposer===document.getElementById('message-composer')&&window.searchTimeline===document.getElementById('message-timeline')),'read-only search preserves composer and timeline nodes');
    assert.equal(await alicePage.locator('#message-composer').inputValue(),searchDraft);
    const sourceLink=searchHit.locator('[data-message-search-source]'),sourceHref=new URL(await sourceLink.getAttribute('href'),base);
    assert.equal(sourceHref.searchParams.get('message'),searchSource.id);
    assert.equal(sourceHref.searchParams.get('account'),alice.id);
    assert.equal(sourceHref.searchParams.get('chat'),aliceConversation.id);
    const sourceGeneration=await alicePage.evaluate(()=>window.OieRuntime.generation);
    await sourceLink.click();
    await alicePage.waitForURL(url=>url.searchParams.get('message')===searchSource.id);
    await alicePage.waitForFunction(generation=>window.OieRuntime.generation>generation,sourceGeneration);
    await alicePage.locator('[data-message-id="'+searchSource.id+'"]').waitFor({state:'attached'});
    assert.equal(new URL(alicePage.url()).searchParams.get('message'),searchSource.id);
    assert.equal(await alicePage.evaluate(()=>window.searchDocument),searchDocument,'source navigation swaps fragments in the existing document');
    assert.equal(await alicePage.locator('#message-composer').inputValue(),searchDraft);
    assert.equal(await alicePage.locator('#conversation-search').inputValue(),archivedIncoming,'source jump retains global query beside the chat');
    assert.equal(await alicePage.locator('[data-message-search-hit][data-search-message="'+searchSource.id+'"]').count(),1,'source jump retains global results beside the selected conversation');
    assert((await alicePage.locator('#message-timeline [data-message-id]').count())<=50,'source jump loads a bounded timeline page');
    await alicePage.locator('#chat-options button').first().click();await alicePage.locator('#chat-search-messages:visible').click();
    await alicePage.locator('#chat-options [role=menu]').waitFor({state:'hidden'});
    await alicePage.locator('#conversation-search-query').fill(archivedIncoming);
    await until(async()=>await alicePage.locator('[data-conversation-search]').getAttribute('data-search-selected')===searchSource.id,'independent per-chat search focuses the real retained source');
    assert.equal(await alicePage.locator('#conversation-search').inputValue(),archivedIncoming,'per-chat query preserves the global result query');
    assert.equal(await alicePage.locator('[data-message-search-hit][data-search-message="'+searchSource.id+'"]').count(),1);
    assert((await alicePage.locator('[data-message-id="'+searchSource.id+'"] mark[data-conversation-search-highlight]:visible').count())>0,'per-chat search highlights current real message text');
    assert.equal(await alicePage.locator('#message-composer').inputValue(),searchDraft);

    for(const [index,conversation] of [aliceConversation,bobConversation].entries())assert.deepEqual(await api('/api/v1/conversations/'+conversation.id+'/messages?limit=100'),searchMessagesBefore[index],'search and source navigation do not mutate retained messages');
    for(const [index,account] of [alice,bob].entries())assert.equal((await api('/api/v1/accounts/'+account.id+'/connection')).state,searchConnectionsBefore[index].state,'search preserves the XMPP connection');
    await alicePage.locator('#message-composer').fill('');
    evidence.workflows.push('Real received XMPP text is searchable through BFF/API/SQLite with one current source per account, retained draft/document, bounded canonical source jump, simultaneous independent per-chat highlight search, and no message or connection effect');

    await alicePage.screenshot({path: 'artifacts/' + prefix + '-alice-desktop.png', fullPage: true});
    await bobPage.setViewportSize({width: 390, height: 844});
    await bobPage.goto(base + '/?chat=' + bobConversation.id + '&account=' + bob.id + '&theme=dark&lang=pt-BR');
    await bobPage.locator('#message-composer').waitFor({state: 'visible'});
    assert(await bobPage.evaluate(() => document.documentElement.scrollWidth <= innerWidth), 'mobile has no horizontal page overflow');
    await bobPage.addScriptTag({path: require.resolve('axe-core/axe.min.js')});
    const a11y = await bobPage.evaluate(async () => window.axe.run(document, {runOnly: {type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa']}}));
    assert.deepEqual(a11y.violations.map(v => ({id: v.id, targets: v.nodes.map(n => n.target)})), [], 'live mobile accessibility');
    await bobPage.screenshot({path: 'artifacts/' + prefix + '-bob-mobile-pt.png', fullPage: true});
    evidence.workflows.push('Actual chat usable at390px in Brazilian Portuguese with no checked WCAG A/AA violations');
    await schedulingFlow({base, api, until, alicePage, bobPage, alice, bob, aliceConversation, bobConversation});
    evidence.workflows.push('Review a scheduled composition with attachment, filter schedules across destinations, edit with draft recovery, send immediately via XMPP and cancel');
    for(let index=evidence.consoleErrors.length-1;index>=0;index--){
      const url=consoleLocations[index],remaining=preferenceConflictURLs.get(url)||0;
      if(remaining>0&&evidence.consoleErrors[index]==='Failed to load resource: the server responded with a status of 409 (Conflict)'){
        preferenceConflictURLs.set(url,remaining-1);
        evidence.expectedTransportFailures.push('Verified reviewed preference CAS conflict: '+url);
        evidence.consoleErrors.splice(index,1);consoleLocations.splice(index,1);
      }
    }
    assert.deepEqual(evidence.consoleErrors, [],JSON.stringify(consoleLocations));
    await fs.writeFile('artifacts/' + prefix + '-report.json', JSON.stringify(evidence, null, 2));
    console.log('PASS: real-engine browser connection, two-way XMPP, source stars across tabs, live drafts, queue/cancel/reconnect, mobile and accessibility');
  } finally { await browser.close(); }
})().catch(error => {console.error(error); process.exitCode = 1;});
