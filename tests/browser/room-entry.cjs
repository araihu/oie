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
const previews = [], commands = [], managementCommands = [], streams = new Set();
let managementRevision = 'first-room-revision', managementBlocked = false, roomImage = avatarHash;
let unavailableBanReads = 0;
const readerAddress = 'leitora@fixture';
const bannedAddresses = new Set(['banido@fixture','sem-foto@fixture',
  ...Array.from({length:30}, (_, index) => 'banido-' + index + '@fixture')]);
let destination, roomState = 'joining', cursor = 0, conflict = false, trustedService = true;

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
    res.writeHead(200, {'Content-Type': 'image/png', 'Content-Length': avatar.length});
    res.end(avatar);
    return;
  }
  if (u.pathname.endsWith('/room-preview')) {
    const scoped = u.searchParams.get('discoveredServiceOnly') === 'true';
    if (scoped && !trustedService) return send(200, {address, state: 'unavailable'});
    previews.push({account: u.pathname.split('/')[4], address: u.searchParams.get('address'), scoped});
    if (u.searchParams.get('address') === 'missing@fixture') return send(200, {address: 'missing@fixture', state: 'not_found'});
    return send(200, {address, state: 'known', name: 'Saberes <script>', description: 'Bem-vindos!', topic: 'Encontro', occupantCount: 17, avatarHash});
  }
  if (req.method === 'GET' && u.pathname.endsWith('/rooms')) {
    return send(200, {state: 'known', rooms: [{address, name: 'Trilha de livros'}]});
  }
  if (u.pathname.endsWith('/room-management')) {
    if (req.method === 'GET') {
      if (u.searchParams.get('affiliation') === 'outcast' && unavailableBanReads > 0) {
        unavailableBanReads--;
        return send(200, {state:'unavailable',actions:[],occupants:[],affiliations:[]});
      }
      return send(200, {state:'available', revision:managementRevision,
        actions:managementBlocked ? ['invite'] : ['rename','topic','invite','role','affiliation','set_avatar','remove_avatar','destroy'],
        occupants:[
          {id:'occupant:reader',nickname:'Leitora',role:'participant',affiliation:'member',avatarHash,roles:['none','visitor','moderator'],affiliations:['outcast']},
          {id:'occupant:visitor',nickname:'Visitante',role:'visitor',affiliation:'none',roles:[],affiliations:[]},
          {id:'self',self:true,nickname:'Marina',role:'moderator',affiliation:'owner',roles:[],affiliations:[]},
          {id:'occupant:admin',nickname:'Helena',role:'moderator',affiliation:'admin',roles:[],affiliations:[]},
          {id:'occupant:moderator',nickname:'Lucas',role:'moderator',affiliation:'member',roles:[],affiliations:[]},
        ].filter(occupant => occupant.id !== 'occupant:reader' || !bannedAddresses.has(readerAddress)),
        affiliations:u.searchParams.get('affiliation') === 'outcast' ? [...bannedAddresses] : [],
        bannedNicknames:u.searchParams.get('affiliation') === 'outcast' && bannedAddresses.has(readerAddress) ? {[readerAddress]:'Leitora'} : undefined});
    }
    let data = '';
    for await (const chunk of req) data += chunk;
    const command = JSON.parse(data);
    managementCommands.push({...command, key:req.headers['idempotency-key']});
    if (managementBlocked || command.expectedRevision !== managementRevision) return send(200, {state:'stale'});
    managementRevision = 'next-room-revision';
    if (command.action === 'set_avatar' || command.action === 'remove_avatar') {
      roomImage = command.action === 'remove_avatar' ? '' : avatarHash;
      cursor++;
      for (const stream of streams) stream.write('id: ' + engine + ':' + cursor + '\nevent: profile.avatar_changed\ndata: {}\n\n');
    }
    if (command.action === 'affiliation' && command.value === 'none') bannedAddresses.delete(command.target);
    if (command.action === 'affiliation' && command.value === 'outcast') {
      bannedAddresses.add(command.target === 'occupant:reader' ? readerAddress : command.target);
    }
    return send(200, {state:'confirmed'});
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
  if (u.pathname.endsWith('/avatars')) {
    const accountId = u.pathname.split('/')[4];
    return send(200, {items: destination?.accountId === accountId
      ? [{accountId, address:destination.address, imageHash:roomImage, updatedAt:at},
         {accountId, address:'banido@fixture', imageHash:avatarHash, updatedAt:at}] : []});
  }
  if (u.pathname.endsWith('/previews') || u.pathname.endsWith('/message-stars')) return send(200, {items: []});
  if (u.pathname === '/api/v1/conversations') return send(200, {items: [source, destination].filter(c => c && (!u.searchParams.has('accountId') || c.accountId === u.searchParams.get('accountId')))});
  if (u.pathname === '/api/v1/conversations/' + chat) return send(200, source);
  if (u.pathname === '/api/v1/conversations/' + room && destination) return send(200, destination);
  if (u.pathname === '/api/v1/conversations/' + room + '/details' && destination) return send(200, {
    accountId:destination.accountId, conversationId:room, kind:'room', address:destination.address,
    name:'Trilha de livros', topic:'Encontro', state:'known', features:[], canInviteToRoom:true,
    accountConnection:{accountId:destination.accountId,state:'connected',updatedAt:at},
    participants:{scope:'occupants',state:'known',truncated:false,items:[]}
  });
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
    const avatarURL = base + '/ui/avatars/' + avatarHash;
    const avatarResponses = [];
    page.on('response', response => {
      if (response.url() === avatarURL) avatarResponses.push(response.status());
    });
    page.on('requestfailed', request => {
      if (request.url() === avatarURL) avatarResponses.push(request.failure()?.errorText);
    });
    const avatarReady = async container => {
      const image = container.locator(`img[src="/ui/avatars/${avatarHash}"]`);
      await image.waitFor({state: 'attached'});
      try {
        await image.evaluate(img => img.decode());
        assert.equal(await image.evaluate(img => img.naturalWidth), 8);
      } catch (error) {
        throw new Error(`Room avatar failed to decode. Responses: ${JSON.stringify(avatarResponses)}`, {cause: error});
      }
    };
    const heldCard = new Promise(resolve => { release = resolve; });
    await page.route('**/ui/rooms/preview', async route => {
      const response = await route.fetch();
      await heldCard;
      await route.fulfill({response});
    }, {times: 1});
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
    assert.equal(previews[0].scoped, true);
    assert.equal(commands.length, 0);
    await page.evaluate(() => {
      const elements = ['#send-message-form', '.oie-timeline-frame', '#message-timeline', '[data-room-bubble]', '.oie-room-invitation-action'].map(s => document.querySelector(s));
      const sample = () => elements.map(el => {
        const box = el.getBoundingClientRect();
        return [box.x, box.y, box.width, box.height, el.scrollTop];
      }).flat();
      let initial = sample();
      window.measureRoomGeometry = () => { initial = sample(); window.roomDrift = 0; };
      window.roomDrift = 0;
      window.trackRoomGeometry = true;
      function frame() {
        for (const [index, value] of sample().entries()) window.roomDrift = Math.max(window.roomDrift, Math.abs(value - initial[index]));
        if (window.trackRoomGeometry) requestAnimationFrame(frame);
      }
      requestAnimationFrame(frame);
    });
    release();
    await card.getByText('Saberes <script>', {exact: true}).waitFor();
    await card.getByText('Bem-vindos!', {exact: true}).waitFor();
    await avatarReady(card);
    assert.equal(await card.locator('script').count(), 0);
    const foreign = await page.request.post(base + '/ui/rooms/preview', {form: {accountId: alice, address}});
    assert.equal(foreign.status(), 403);
    previews.length = 0;
    const heldPreview = new Promise(resolve => { release = resolve; });
    await page.route('**/ui/rooms/preview', async route => {
      const response = await route.fetch();
      await heldPreview;
      await route.fulfill({response}).catch(() => {});
    }, {times: 2});
    assert.equal(await page.evaluate(() => window.roomDrift), 0);
    await link.focus();
    await page.keyboard.press('Enter');
    const form = page.locator('#join-room-form');
    const preview = form.locator('[data-room-preview]');
    await preview.getByText('Carregando prévia da sala…').waitFor();
    await page.evaluate(() => window.measureRoomGeometry());
    assert.equal(new URL(page.url()).searchParams.get('chat'), chat);
    assert.equal(new URL(page.url()).searchParams.has('open'), false);
    assert.equal(await form.locator('[name=address]').inputValue(), address);
    assert.equal(await form.locator('[name=accountId]').inputValue(), alice);
    assert.equal(commands.length, 0);
    await form.getByRole('combobox').click();
    await form.getByRole('option', {name: 'Bob · bob@fixture'}).click();
    await waitFor(() => previews.length === 2);
    assert.deepEqual(previews.map(value => value.account), [alice, bob]);
    assert(previews.every(value => !value.scoped));
    release();
    await preview.getByText('Saberes <script>', {exact: true}).waitFor();
    assert.equal(await preview.locator('script').count(), 0);
    await avatarReady(preview);
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
    await page.route('**/ui/rooms/open', async route => {
      await route.fetch();
      await route.abort();
    }, {times: 1});
    await form.getByRole('button', {name: 'Entrar na sala', exact: true}).click();
    await waitFor(() => commands.length === 1);
    await page.reload();
    await page.locator('body[data-oie-live-ready=true]').waitFor();
    await page.locator('.oie-rendered-message [data-room-invitation]').click();
    assert.equal(await form.locator('[name=nickname]').inputValue(), 'Leitora');
    assert.equal(await form.locator('[name=nickname]').getAttribute('readonly'), '');
    assert.equal(await form.locator('[name=accountId]').inputValue(), bob);
    const joined = page.waitForResponse(res => res.url() === base + '/ui/rooms/open');
    await form.locator('button[type=submit]').click();
    const joinResponse = await joined;
    assert.equal(joinResponse.status(), 200, await joinResponse.text());
    await page.waitForURL(url => url.searchParams.get('chat') === room).catch(async error => {
      throw new Error(JSON.stringify({url: page.url(), response: await joinResponse.json(), commands, errors}), {cause: error});
    });
    await page.locator('#room-session-status[data-room-state=joining]').waitFor();
    assert.deepEqual(commands[1], commands[0]);
    assert.deepEqual(commands.map(({key, ...command}) => command), Array(2).fill({accountId: bob, address, action: 'join', nickname: 'Leitora', name: ''}));
    roomState = 'joined';
    cursor++;
    for (const stream of streams) stream.write('id: ' + engine + ':' + cursor + '\nevent: conversation.room_session_changed\ndata: {}\n\n');
    await page.locator('#room-session-status').waitFor({state: 'hidden'});
    for (const width of [1440, 390]) {
      await page.setViewportSize({width, height: 900});
      let finishOpening;
      const opening = new Promise(resolve => {finishOpening = resolve;});
      if (width === 1440) {
        await page.route('**/ui/rooms/manage-fragment?*',async route => {
          await opening;
          await route.continue();
        },{times:1});
      }
      if (width === 1440) {
        await page.locator('#chat-header .oie-chat-avatar-trigger').click();
        await page.locator('[data-info-manage-room]').click();
      } else {
        await page.locator('[aria-controls=chat-options-panel]').click();
        await page.locator('#chat-manage-room').click();
      }
      const management = page.locator('[data-room-management]');
      const openingSkeleton = management.getByRole('status',{name:'Carregando ações da sala…',exact:true});
      if (width === 1440) {
        await openingSkeleton.waitFor();
        assert.equal(await openingSkeleton.locator('[data-skeleton-item]').count(),5);
        finishOpening();
      }
      const headerBack = page.getByRole('button',{name:'Voltar aos detalhes da conversa',exact:true});
      await management.getByRole('tablist').waitFor();
      if (width === 1440) {
        const back = await headerBack.boundingBox();
        assert(back.width >= 44 && back.height >= 44, 'Back has a touch-sized target');
        assert(await headerBack.evaluate(button => !!button.closest('header')), 'Back belongs in the header');
        await headerBack.focus();
        await page.getByRole('tooltip',{name:'Voltar aos detalhes da conversa'}).waitFor();
        await headerBack.press('Enter');
        const manageEntry = page.locator('[data-info-manage-room]:visible');
        await manageEntry.waitFor();
        await page.waitForFunction(() => document.activeElement?.matches('[data-info-manage-room]'));
        await manageEntry.press('Enter');
      } else {
        assert.equal(await headerBack.isVisible(), false, 'Direct menu entry has no parent');
      }
      await management.locator('[data-details-edit]:visible').click();
      const saveDetails = management.locator('[data-details-review]');
      await saveDetails.waitFor();
      assert.equal(await management.locator('[data-room-participant-detail]').count(),0);
      assert.equal(await management.locator('[data-room-profile-slot] form').count(),0);
      await openingSkeleton.waitFor({state:'hidden'});
      assert.equal(await management.getByRole('button',{name:'Atualizar',exact:true}).count(),0);
      assert.equal(await saveDetails.isDisabled(), true);
      const topic = management.getByRole('textbox', {name:'Tópico', exact:true});
      const originalTopicField = await topic.elementHandle();
      assert.equal(await topic.evaluate(input => input.tagName), 'TEXTAREA');
      const originalTopic = await topic.inputValue();
      const name = management.getByRole('textbox',{name:'Nome da sala',exact:true});
      const originalName = await name.inputValue();
      const saveButton = await saveDetails.elementHandle();
      const rightEdgeOffset = await topic.evaluate((input, button) => {
        return button.getBoundingClientRect().right - input.getBoundingClientRect().right;
      }, saveButton);
      await saveButton.dispose();
      assert(Math.abs(rightEdgeOffset) <= 1, `Save and topic right edges differ by ${rightEdgeOffset}px`);
      await name.fill(originalName + ' ');
      assert.equal(await saveDetails.isDisabled(), true);
      await name.fill('Outro nome');
      assert.equal(await saveDetails.isEnabled(), true);
      await name.fill(originalName);
      assert.equal(await saveDetails.isDisabled(), true);
      await topic.fill('Outro tópico');
      assert.equal(await saveDetails.isEnabled(), true);
      await topic.fill(originalTopic);
      assert.equal(await saveDetails.isDisabled(), true);
      const detailsTab = management.getByRole('tab', {name:'Detalhes', exact:true});
      const participantsTab = management.getByRole('tab', {name:'Participantes', exact:true});
      const participant = management.getByRole('button', {name:'Leitora', exact:true});
      await management.locator('.oie-room-management-avatar [data-avatar-ready=true]').waitFor();
      assert.equal(await detailsTab.getAttribute('aria-selected'), 'true');
      assert.equal(await participant.isVisible(), false);
      await detailsTab.focus();
      await page.keyboard.press('ArrowRight');
      await page.keyboard.press('Enter');
      await participant.waitFor();
      assert.equal(await participantsTab.getAttribute('aria-selected'), 'true');
      assert.equal(await topic.isVisible(), false);
      assert.deepEqual(await management.locator('.oie-room-participant-group h3').allTextContents(),
        ['Proprietários','Administradores','Moderadores','Participantes','Visitantes']);
      await management.getByRole('button',{name:'Marina (Você)',exact:true}).waitFor();
      const photo = participant.locator('img');
      await photo.waitFor();
      assert.equal(await photo.getAttribute('src'), '/ui/avatars/'+avatarHash);
      const search = management.getByRole('searchbox',{name:'Pesquisar participantes',exact:true});
      await search.fill('  LEITÓ  ');
      assert.deepEqual(await management.locator('.oie-room-participant-group:visible h3').allTextContents(),['Participantes']);
      await search.fill('sem resultado');
      await management.getByText('Nenhum participante encontrado.',{exact:true}).waitFor();
      await page.keyboard.press('Escape');
      assert.equal(await search.inputValue(),'');
      await search.fill('leit');
      let finishProfile;
      if (width === 1440) {
        const profileLoading = new Promise(resolve => {finishProfile = resolve;});
        await page.route(url => url.pathname === '/ui/rooms/manage-fragment' && url.searchParams.has('profile'), async route => {
          await profileLoading;
          await route.continue();
        },{times:1});
      }
      await participant.focus();
      await page.keyboard.press('Enter');
      if (finishProfile) {
        await management.locator('.oie-room-profile-skeleton').waitFor();
        assert.equal(await management.locator('[data-room-participant-detail]').count(),0);
        finishProfile();
      }
      const profile = management.getByRole('region',{name:'Leitora',exact:true});
      await profile.getByRole('heading',{name:'Leitora',exact:true}).waitFor();
      assert.equal(await management.locator('[data-room-participant-detail]').count(),1);
      assert.equal(await profile.getByRole('button',{name:'Tornar moderador',exact:true}).isVisible(),true);
      assert.equal(await profile.getByRole('button',{name:'Banir da sala',exact:true}).isVisible(),true);
      assert.equal(await participant.isVisible(),false);
      assert.equal(await search.isVisible(),false);
      await page.keyboard.press('Escape');
      await profile.waitFor({state:'hidden'});
      assert.equal(await participant.evaluate(el => document.activeElement === el),true);
      assert.equal(await search.inputValue(),'leit');
      await participant.click();
      const kickTrigger = profile.getByRole('button', {name:'Remover da sala',exact:true});
      const promote = profile.getByRole('button', {name:'Tornar moderador',exact:true});
      await promote.waitFor();
      assert.equal(await promote.locator('.oie-room-action-icon svg').count(), 1);
      assert.equal(await kickTrigger.locator('..').getAttribute('data-tone'), 'danger');
      await promote.click();
      const promoteForm = profile.locator('form[data-room-action-form$=role-moderator]');
      await promoteForm.getByRole('button',{name:'Confirmar',exact:true}).waitFor();
      assert.equal(await promote.getAttribute('aria-expanded'), 'true');
      await page.keyboard.press('Escape');
      await promoteForm.waitFor({state:'hidden'});
      assert.equal(await promote.getAttribute('aria-expanded'), 'false');
      assert.equal(await promote.evaluate(el => document.activeElement === el), true);
      await kickTrigger.click();
      const kick = management.locator('form[data-room-action-form$=role-none]');
      await kick.getByRole('textbox',{name:'Motivo (opcional)'}).waitFor();
      assert.equal(await kickTrigger.getAttribute('aria-expanded'), 'true');
      await kick.getByRole('button',{name:'Cancelar'}).click();
      await kick.waitFor({state:'hidden'});
      assert.equal(await kickTrigger.getAttribute('aria-expanded'), 'false');
      await page.getByRole('button',{name:'Voltar para participantes'}).click();
      await participant.waitFor();
      assert.deepEqual((await management.getByRole('tab').allTextContents()).map(label => label.trim()),
        ['Detalhes','Participantes','Banimentos']);
      const bans = management.getByRole('tabpanel',{name:'Banimentos',exact:true});
      const bansTab = management.getByRole('tab',{name:'Banimentos',exact:true});
      let finishBans;
      const bansLoading = new Promise(resolve => {finishBans = resolve;});
      const bansSkeleton = bans.getByRole('status',{name:'Carregando ações da sala…',exact:true});
      const tabsBeforeBans = await management.getByRole('tablist').boundingBox();
      const originalParticipant = await participant.elementHandle();
      if (width === 1440) {
        await page.route(url => url.pathname === '/ui/rooms/manage-fragment' && url.searchParams.get('affiliation') === 'outcast',async route => {
          await bansLoading;
          await route.fulfill({status:503});
        },{times:1});
        await bansTab.click();
        await bansSkeleton.waitFor();
        assert.deepEqual(await management.getByRole('tablist').boundingBox(),tabsBeforeBans);
        finishBans();
        const errorToast = page.locator('[data-toast-container]').getByRole('alert').filter({hasText:'As ações da sala estão indisponíveis.'});
        await errorToast.waitFor();
        await bansSkeleton.waitFor({state:'hidden'});
        await errorToast.getByRole('button',{name:'Fechar',exact:true}).click();
      }
      await bansTab.click();
      await bans.getByRole('button',{name:'banido@fixture',exact:true}).waitFor();
      await bansSkeleton.waitFor({state:'hidden'});
      assert.deepEqual(await management.getByRole('tablist').boundingBox(),tabsBeforeBans);
      const bannedAvatar = bans.locator('.oie-room-addresses [data-avatar-address="banido@fixture"]');
      await bannedAvatar.locator('[data-avatar-ready=true] img').waitFor();
      const initials = bans.locator('.oie-room-addresses [data-avatar-address="sem-foto@fixture"] [data-avatar-initials]');
      await initials.waitFor();
      assert.equal((await initials.innerText()).trim(),'SF');
      assert.equal(await bans.getByRole('button',{name:'banido@fixture',exact:true}).isVisible(),true);
      assert.equal(await management.locator('[data-room-participant-detail]').count(),0);
      assert(await originalParticipant.evaluate(node => node.isConnected));
      assert(await originalTopicField.evaluate(node => node.isConnected));
      await participantsTab.click();
      assert.equal(await search.inputValue(),'leit');
      assert.deepEqual(await management.locator('.oie-room-participant-group:visible h3').allTextContents(),['Participantes']);
      await search.clear();
      await detailsTab.click();

      if (width === 1440 && process.env.OIE_ROOM_MANAGEMENT_SCREENSHOT) {
        await management.screenshot({path:process.env.OIE_ROOM_MANAGEMENT_SCREENSHOT});
      }
      if (width === 1440) {
        const releaseRequest = {};
        const held = new Promise(resolve => {releaseRequest.send = resolve;});
        await page.route('**/ui/rooms/manage', async route => {
          await held;
          await route.continue();
        }, {times:1});
        await management.getByRole('textbox',{name:'Nome da sala',exact:true}).fill('Leituras de sábado');
        await topic.fill('Próxima leitura\nTraga um conto');
        const geometry = () => page.locator('#send-message-form').evaluate(element => {
          const {x,y,width,height} = element.getBoundingClientRect();
          return [x,y,width,height];
        });
        const before = await geometry();
        const tabsBefore = await management.getByRole('tablist').boundingBox();
        await saveDetails.click();
        assert.equal(managementCommands.length, 0);
        await management.locator('[data-details-stage=review] button[type=submit]').click();
        assert.equal(await saveDetails.isDisabled(), true);
        assert.deepEqual(await geometry(), before);
        releaseRequest.send();
        const toast = page.locator('[data-toast-container]').getByRole('alert').filter({hasText:'Alteração confirmada.'});
        await toast.waitFor();
        assert.equal(await management.getByText('Alteração confirmada.',{exact:true}).count(),0);
        assert.deepEqual(await management.getByRole('tablist').boundingBox(),tabsBefore);
        await toast.getByRole('button',{name:'Fechar',exact:true}).click();
        await toast.waitFor({state:'hidden'});
        assert.equal(await saveDetails.isDisabled(), true);
        await management.locator('[data-details-edit]:visible').click();
        await topic.fill('Próxima leitura\nTraga um conto\n');
        assert.equal(await saveDetails.isDisabled(), true);
        await topic.fill('Próxima leitura\nTraga dois contos');
        assert.equal(await saveDetails.isEnabled(), true);
        await topic.fill('Próxima leitura\nTraga um conto');
        assert.equal(managementCommands.length, 1);
        assert.equal(managementCommands[0].action, 'update_details');
        assert.equal(managementCommands[0].name, 'Leituras de sábado');
        assert.equal(managementCommands[0].topic, 'Próxima leitura\nTraga um conto');
        assert.equal(await management.getByRole('button',{name:'Enviar convite'}).count(),0);
        assert.deepEqual(await geometry(), before);
      } else {
        await topic.fill('Rascunho do tópico');
        managementBlocked = true;
        await saveDetails.click();
        const submit = management.locator('[data-details-stage=review] button[type=submit]');
        await submit.click();
        await management.getByText('A sala mudou. Revise as alterações antes de tentar novamente.', {exact:true}).waitFor();
        await waitFor(() => managementCommands.length === 2);
        assert.equal(managementCommands[1].topic, 'Rascunho do tópico');
        assert.equal(managementCommands[1].name, undefined);
        assert.equal(await topic.inputValue(), 'Rascunho do tópico');
        await waitFor(() => submit.isDisabled());
      }
      await page.keyboard.press('Escape');
      await management.waitFor({state:'hidden'});
    }

    managementBlocked = false;
    await page.locator('[aria-controls=chat-options-panel]').click();
    await page.locator('#chat-manage-room').click();
    const recoveredPanel = page.locator('[data-room-management]');
    await recoveredPanel.getByRole('textbox',{name:'Tópico',exact:true}).waitFor();
    await waitFor(() => recoveredPanel.locator('[data-details-review]').isEnabled());
    assert.equal(await recoveredPanel.getByRole('textbox',{name:'Tópico',exact:true}).inputValue(),'Rascunho do tópico');
    await page.keyboard.press('Escape');
    await recoveredPanel.waitFor({state:'hidden'});

    await page.locator('[aria-controls=chat-options-panel]').click();
    await page.getByRole('menuitem',{name:'Convidar pessoas'}).click();
    const invitePanel = page.locator('[data-room-management]');
    await page.getByRole('heading',{name:'Convidar pessoas'}).waitFor();
    assert.equal(await invitePanel.getByRole('button',{name:'Salvar alterações'}).count(),0);
    await invitePanel.getByRole('textbox',{name:'Endereço da pessoa'}).fill('amiga@fixture');
    await invitePanel.getByRole('button',{name:'Enviar convite'}).click();
    await waitFor(() => managementCommands.length === 3);
    assert.equal(managementCommands[2].action,'invite');
    assert.equal(managementCommands[2].target,'amiga@fixture');
    await page.keyboard.press('Escape');
    await invitePanel.waitFor({state:'hidden'});

    await page.locator('[aria-controls=chat-options-panel]').click();
    await page.locator('#chat-manage-room').click();
    const roomPanel = page.locator('[data-room-management]');
    const dismissRoomConfirmations = async () => {
      const toasts = page.locator('[data-toast-container]').getByRole('alert').filter({hasText:'Alteração confirmada.'});
      while (await toasts.first().isVisible()) {
        const toast = await toasts.first().elementHandle();
        // The room dialog traps keyboard focus, and a new confirmation can
        // arrive before the dismissed toast finishes its transition.
        await toast.$eval('button[aria-label="Fechar"]', button => button.click());
        await toast.waitForElementState('hidden');
      }
    };
    const image = roomPanel.locator('.oie-room-management-avatar [data-avatar-ready=true]');
    await image.waitFor();
    const destructiveActions = roomPanel.locator('[aria-controls="room-destructive-actions-body"]');
    const destructiveDrawer = page.getByRole('dialog', {name:'Ações destrutivas',exact:true});
    const destroyToggle = page.locator('#room-destroy-open');
    const destroyForm = page.locator('#room-destroy-form');
    const beforeDestroy = managementCommands.length;
    await destroyToggle.waitFor({state:'hidden'});
    await destructiveActions.press('Enter');
    await destructiveDrawer.waitFor();
    await roomPanel.waitFor({state:'hidden'});
    await destroyToggle.click();
    await destroyForm.waitFor({state:'visible'});
    assert.equal(await destroyToggle.isVisible(),false);
    await destructiveDrawer.getByRole('button',{name:'Voltar aos detalhes',exact:true}).click();
    await destructiveDrawer.waitFor({state:'hidden'});
    await roomPanel.waitFor({state:'visible'});
    await destructiveActions.click();
    await destroyToggle.waitFor({state:'visible'});
    assert.equal(await destroyForm.isVisible(),false,'reopening the subpage requires a fresh confirmation');
    await destructiveDrawer.getByRole('button',{name:'Voltar aos detalhes',exact:true}).click();
    await roomPanel.waitFor({state:'visible'});
    assert.equal(managementCommands.length,beforeDestroy);
    for (const nextRevision of [managementRevision, 'after-destroy-review']) {
      const reviewedRevision = await destroyForm.locator('[name=expectedRevision]').inputValue();
      await page.keyboard.press('Escape');
      await roomPanel.waitFor({state:'hidden'});
      const requested = Promise.withResolvers();
      const releaseRefresh = Promise.withResolvers();
      await page.route('**/ui/rooms/manage-fragment?**', async route => {
        requested.resolve();
        await releaseRefresh.promise;
        await route.continue();
      }, {times:1});
      try {
        await page.locator('[aria-controls=chat-options-panel]').click();
        await page.locator('#chat-manage-room').click();
        await requested.promise;
        await destructiveActions.click();
        await destroyToggle.click();
        await page.waitForFunction(() => document.activeElement.matches('#room-destroy-form [data-room-action-cancel]'));
        managementRevision = nextRevision;
      } finally {
        releaseRefresh.resolve();
      }
      await page.waitForFunction(() => !document.querySelector('[data-room-management]').hasAttribute('aria-busy'));
      if (nextRevision === reviewedRevision) {
        assert.equal(await destroyForm.isVisible(),true);
        assert(await destroyForm.locator('[data-room-action-cancel]').evaluate(button => document.activeElement === button));
      } else {
        assert.equal(await destroyForm.isVisible(),false,'a changed revision requires a new confirmation');
        assert(await destroyToggle.evaluate(button => document.activeElement === button));
        const conflict = page.locator('[data-toast-container]').getByRole('alert').filter({hasText:'A sala mudou.'});
        await conflict.waitFor();
        await conflict.locator('button[aria-label="Fechar"]').evaluate(button => button.click());
        await conflict.waitFor({state:'hidden'});
      }
      assert.equal(await destroyForm.locator('[name=expectedRevision]').inputValue(),nextRevision);
      assert.equal(managementCommands.length,beforeDestroy);
      await destructiveDrawer.getByRole('button',{name:'Voltar aos detalhes',exact:true}).click();
      await roomPanel.waitFor({state:'visible'});
    }
    await roomPanel.getByRole('button',{name:'Remover imagem da sala',exact:true}).click();
    await roomPanel.locator('#room-avatar-removal').getByRole('button',{name:'Remover imagem da sala',exact:true}).click();
    await image.waitFor({state:'hidden'});
    assert.equal(managementCommands.at(-1).action,'remove_avatar');
    await roomPanel.getByRole('button',{name:'Trocar imagem da sala',exact:true}).click();
    await roomPanel.locator('input[name=image]').setInputFiles({name:'room.png',mimeType:'image/png',buffer:avatar});
    await roomPanel.locator('#room-avatar-upload').getByRole('button',{name:'Trocar imagem da sala',exact:true}).click();
    await image.waitFor();
    assert.equal(managementCommands.at(-1).action,'set_avatar');
    assert.equal(managementCommands.at(-1).image,avatar.toString('base64'));
    await dismissRoomConfirmations();
    await roomPanel.getByRole('tab',{name:'Banimentos',exact:true}).click();
    const banSearch = roomPanel.getByRole('searchbox',{name:'Pesquisar banimentos',exact:true});
    const bansList = roomPanel.locator('.oie-room-bans-list [data-goshtoso-scroll-viewport]');
    await bansList.getByText('sem-foto@fixture',{exact:true}).waitFor();
    await banSearch.fill('  SEM-FOTO@  ');
    assert.deepEqual(await bansList.locator('li:visible').getByText('sem-foto@fixture',{exact:true}).allTextContents(),['sem-foto@fixture']);
    assert.equal(await bansList.locator('li:visible').count(),1);
    await banSearch.fill('sem correspondência');
    await roomPanel.getByText('Nenhum endereço encontrado.',{exact:true}).waitFor();
    await banSearch.press('Escape');
    assert.equal(await banSearch.inputValue(),'');
    await banSearch.fill('banido@');
    const bannedRow = roomPanel.getByRole('button',{name:'banido@fixture',exact:true});
    const bannedProfile = roomPanel.getByRole('region',{name:'banido@fixture',exact:true});
    const beforeUnban = managementCommands.length;
    await bannedRow.press('Enter');
    await bannedProfile.getByRole('heading',{name:'banido@fixture',exact:true}).waitFor();
    await bannedProfile.getByText('Banido',{exact:true}).waitFor();
    assert.equal(await banSearch.isVisible(),false);
    assert.equal(managementCommands.length,beforeUnban);
    await page.getByRole('button',{name:'Voltar para banimentos'}).click();
    assert.equal(await banSearch.inputValue(),'banido@');
    assert.equal(await bannedRow.evaluate(el => document.activeElement === el),true);
    await bannedRow.click();
    const unban = bannedProfile.getByRole('button',{name:'Retirar banimento',exact:true});
    await unban.click();
    const confirmation = bannedProfile.locator('form[data-room-action-confirmation]');
    await confirmation.getByText('Retirar o banimento de banido@fixture?',{exact:true}).waitFor();
    const cancelUnban = confirmation.getByRole('button',{name:'Cancelar',exact:true});
    await page.waitForFunction(() => document.activeElement?.matches('[data-room-action-confirmation] [data-room-action-cancel]'));
    assert.equal(managementCommands.length,beforeUnban);
    await cancelUnban.click();
    await confirmation.waitFor({state:'hidden'});
    assert.equal(await unban.evaluate(el => document.activeElement === el),true);
    await unban.click();
    await page.waitForFunction(() => document.activeElement?.matches('[data-room-action-confirmation] [data-room-action-cancel]'));
    await page.keyboard.press('Escape');
    await confirmation.waitFor({state:'hidden'});
    assert.equal(managementCommands.length,beforeUnban);
    await unban.click();
    await confirmation.getByRole('button',{name:'Confirmar',exact:true}).click();
    await bannedProfile.waitFor({state:'hidden'});
    await bannedRow.waitFor({state:'detached'});
    assert.equal(await banSearch.inputValue(),'banido@');
    await roomPanel.getByText('Nenhum endereço encontrado.',{exact:true}).waitFor();
    await banSearch.clear();
    assert.equal(await bansList.getByText('sem-foto@fixture',{exact:true}).isVisible(),true);
    assert.equal(managementCommands.length,beforeUnban+1);
    assert.equal(managementCommands.at(-1).action,'affiliation');
    assert.equal(managementCommands.at(-1).target,'banido@fixture');
    await dismissRoomConfirmations();
    assert.equal(managementCommands.at(-1).value,'none');
    const banTabs = roomPanel.getByRole('tablist');
    const banToggle = roomPanel.getByRole('button',{name:'Banir por endereço',exact:true});
    await banToggle.hover();
    const banHelp = page.getByRole('tooltip').filter({hasText:'Banir por endereço'});
    await banHelp.getByText('Impeça alguém de entrar na sala pelo endereço, mesmo que nunca tenha participado.').waitFor();
    await banSearch.hover();
    await banHelp.waitFor({state:'hidden'});
    assert.equal(await banToggle.getAttribute('aria-controls'),'room-ban-by-address-form');
    const banAddress = roomPanel.getByRole('textbox',{name:'Endereço da pessoa',exact:true});
    assert.equal(await banAddress.isVisible(),false);
    const footerBeforeScroll = await banToggle.boundingBox();
    const searchBeforeScroll = await banSearch.boundingBox();
    const tabsBeforeScroll = await banTabs.boundingBox();
    const scrollTop = await bansList.evaluate(list => {
      list.scrollTop = 200;
      return list.scrollTop;
    });
    assert.equal(scrollTop,200);
    assert.deepEqual(await banToggle.boundingBox(),footerBeforeScroll);
    assert.deepEqual(await banSearch.boundingBox(),searchBeforeScroll);
    assert.deepEqual(await banTabs.boundingBox(),tabsBeforeScroll);
    const beforeBan = managementCommands.length;
    await banToggle.hover();
    await banHelp.waitFor({state:'visible'});
    await banToggle.click();
    await banAddress.waitFor({state:'visible'});
    await banHelp.waitFor({state:'hidden'});
    await banToggle.waitFor({state:'hidden'});
    await banAddress.fill('ainda-fora@fixture');
    await page.keyboard.press('Escape');
    await banAddress.waitFor({state:'hidden'});
    await banToggle.waitFor({state:'visible'});
    assert.equal(await banToggle.evaluate(el => document.activeElement === el),true);
    assert.equal(managementCommands.length,beforeBan);
    await banToggle.click();
    await banAddress.fill('ainda-fora@fixture');
    const topicBeforeBan = await roomPanel.locator('textarea[name=topic]').elementHandle();
    const rowsBeforeBan = await roomPanel.locator('#room-participant-results').elementHandle();
    await roomPanel.getByRole('button',{name:'Banir da sala',exact:true}).click();
    await roomPanel.getByRole('button',{name:'ainda-fora@fixture',exact:true}).waitFor();
    await banToggle.waitFor({state:'visible'});
    assert(await topicBeforeBan.evaluate(node => node.isConnected));
    assert(await rowsBeforeBan.evaluate(node => node.isConnected));
    assert.equal(await banAddress.isVisible(),false);
    assert.equal(await bansList.evaluate(list => list.scrollTop),scrollTop);
    assert.equal(managementCommands.at(-1).value,'outcast');
    assert.equal(managementCommands.at(-1).target,'ainda-fora@fixture');
    assert.equal(managementCommands.length,beforeBan+1);
    await dismissRoomConfirmations();

    const bansTab = roomPanel.getByRole('tab',{name:'Banimentos',exact:true});
    await roomPanel.getByRole('tab',{name:'Participantes',exact:true}).click();
    await roomPanel.getByRole('button',{name:'Leitora',exact:true}).click();
    const reader = roomPanel.getByRole('region',{name:'Leitora',exact:true});
    await reader.getByRole('button',{name:'Banir da sala',exact:true}).click();
    unavailableBanReads = 2;
    await reader.getByRole('button',{name:'Confirmar',exact:true}).click();
    const reloadError = page.locator('[data-toast-container]').getByRole('alert').filter({hasText:'As ações da sala estão indisponíveis.'});
    await reloadError.waitFor();
    assert.equal(unavailableBanReads,0);
    await reloadError.getByRole('button',{name:'Fechar',exact:true}).click();
    await reloadError.waitFor({state:'hidden'});
    await dismissRoomConfirmations();
    assert.equal(managementCommands.at(-1).target,'occupant:reader');
    assert.equal(managementCommands.at(-1).value,'outcast');
    await bansTab.press('Enter');
    const bannedReader = roomPanel.getByRole('button',{name:'Leitora '+readerAddress,exact:true});
    await bannedReader.waitFor();
    const listedAddress = bannedReader.getByText(readerAddress,{exact:true});
    await listedAddress.waitFor();
    const nicknameBox = await bannedReader.getByText('Leitora',{exact:true}).boundingBox();
    assert((await listedAddress.boundingBox()).y >= nicknameBox.y + nicknameBox.height - 1);
    await banSearch.fill('leitora');
    assert.equal(await bansList.locator('li:visible').count(),1);
    await banSearch.fill(readerAddress);
    await bannedReader.click();
    const bannedReaderProfile = roomPanel.getByRole('region',{name:'Leitora',exact:true});
    await bannedReaderProfile.getByText(readerAddress,{exact:true}).waitFor();
    await page.getByRole('button',{name:'Voltar para banimentos'}).click();
    await roomPanel.getByRole('tab',{name:'Participantes',exact:true}).click();
    await roomPanel.locator('#room-participant-results').getByRole('button',{name:'Leitora',exact:true}).waitFor({state:'detached'});
    assert.equal(managementCommands.length,beforeBan+2);
    await page.keyboard.press('Escape');
    await roomPanel.waitFor({state:'hidden'});

    await page.setViewportSize({width:1440,height:950});
    conflict = true;
    await page.goto(base + '/?' + new URLSearchParams({chat, account: alice, lang: 'en'}));
    await page.locator('body[data-oie-live-ready=true]').waitFor();
    await page.locator('[data-new-conversation]').click();
    const newMenu = page.locator('#new-conversation-menu').getByRole('menu');
    await newMenu.waitFor({state:'visible'});
    assert.deepEqual((await newMenu.getByRole('menuitem').allTextContents()).map(label=>label.trim()), ['Direct conversation','Join room','Create room']);
    await newMenu.getByRole('menuitem', {name:'Direct conversation'}).click();
    await page.locator('#new-conversation-form').waitFor();
    await newMenu.waitFor({state:'hidden'});
    await page.keyboard.press('Escape');
    await page.locator('#newchat-body').waitFor({state:'hidden'});
    await page.waitForFunction(()=>document.activeElement?.hasAttribute('data-new-conversation'));
    await page.keyboard.press('Enter');
    await newMenu.getByRole('menuitem', {name:'Join room'}).click();
    await page.locator('#join-room-form').waitFor();
    await page.locator('#newchat-body').waitFor({state:'hidden'});
    await page.keyboard.press('Escape');
    await page.locator('#join-room-body').waitFor({state:'hidden'});
    await page.locator('[data-new-conversation]').click();
    await newMenu.getByRole('menuitem', {name:'Create room'}).click();

    const create = page.locator('#create-room-form');
    await create.locator('[name=address]').fill('new@rooms.fixture');
    await create.locator('[name=name]').fill('A new room');
    await create.getByRole('button', {name: 'Create room'}).click();
    await create.getByText('Room state changed. Refresh before trying again.').waitFor();
    assert.equal(await create.locator('[name=name]').inputValue(), 'A new room');
    conflict = false;
    await create.getByRole('button', {name: 'Create room'}).click();
    await page.waitForURL(url => url.searchParams.get('chat') === room);
    assert.equal(commands[2].key, commands[3].key);
    assert.equal(commands[3].action, 'create');
    assert.equal(commands[3].name, 'A new room');

    await page.goto(base + '/?' + new URLSearchParams({chat, account: alice, open: 'join-room', lang: 'en'}));
    await form.waitFor();
    const previewButton = form.getByRole('button', {name: 'Preview room'});
    const directory = form.locator('[data-room-directory]');
    await directory.locator('summary').click();
    await directory.getByRole('button', {name:'Browse public rooms'}).click();
    await directory.getByRole('button', {name:'Trilha de livros'}).click();
    assert.equal(await form.locator('[name=address]').inputValue(), address);
    assert.equal(commands.length, 4);
    await directory.locator('summary').click();
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
    assert.equal(commands.length, 4);

    const fallback = await browser.newPage({viewport: {width: 390, height: 860}, locale: 'en'});
    fallback.on('pageerror', error => errors.push(error.message));
    trustedService = false;
    const beforeFallback = previews.length;
    await fallback.goto(base + '/?' + new URLSearchParams({chat, account: alice, lang: 'en'}));
    const fallbackCard = fallback.locator('[data-room-bubble]');
    await fallbackCard.getByText('Room invitation', {exact: true}).waitFor();
    const fallbackLink = fallbackCard.getByRole('link', {name: /View room/});
    const href = await fallbackLink.getAttribute('href');
    const params = new URL(href, base).searchParams;
    assert.equal(params.get('chat'), chat);
    assert.equal(params.get('account'), alice);
    assert.equal(params.get('lang'), 'en');
    assert.equal(previews.length, beforeFallback);
    await fallback.goto(base + href);
    const newTabForm = fallback.locator('#join-room-form');
    await newTabForm.waitFor();
    assert.equal(await newTabForm.locator('[name=accountId]').inputValue(), alice);
    assert.equal(await newTabForm.locator('[name=address]').inputValue(), address);
    assert.equal(previews.length, beforeFallback);
    await newTabForm.getByRole('button', {name: 'Preview room'}).click();
    await newTabForm.getByText('Bem-vindos!').waitFor();
    await fallbackCard.getByText('Saberes <script>', {exact: true}).waitFor();
    assert.equal(previews.length, beforeFallback + 1);
    assert.equal(commands.length, 4);
    await fallback.close();
    trustedService = true;

    const artifacts = path.join(__dirname, 'artifacts');
    await fs.mkdir(artifacts, {recursive: true});

    for (const width of [390, 1440]) {
      for (const theme of ['goshtoso', 'minimal']) {
        for (const mode of ['light', 'dark']) {
          const view = await browser.newPage({viewport: {width, height: 900}, locale: 'en'});
          view.on('pageerror', error => errors.push(error.message));
          await view.goto(base + '/?' + new URLSearchParams({chat, account: alice, open: 'join-room', room: address, theme: mode, lang: 'en'}));
          await view.evaluate(value => { document.documentElement.dataset.theme = value; }, theme);
          await view.locator('#join-room-form').getByRole('button', {name: 'Preview room'}).click();
          await view.locator('#join-room-form').getByText('Bem-vindos!').waitFor();
          await view.locator('[data-room-bubble]').getByText('Saberes <script>', {exact: true}).waitFor();
          assert(await view.evaluate(() => document.documentElement.scrollWidth <= innerWidth));
          await view.addScriptTag({path: require.resolve('./fixtures/axe-core/axe.min.js')});
          const scan = id => view.evaluate(selector => window.axe.run(selector,
            {runOnly: {type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa']}}), id);
          const cardIssues = await scan('[data-room-bubble]');
          assert.deepEqual(cardIssues.violations.map(value => value.id), [], ['card', width, theme, mode].join(' '));
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
          await view.locator('#new-conversation-menu').getByRole('menuitem', {name:'Create room'}).click();
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
