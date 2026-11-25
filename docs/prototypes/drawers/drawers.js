
'use strict';
const $ = id => document.getElementById(id);
const params = new URLSearchParams(location.search);
let english = params.get('lang') === 'en';
const desktop = params.get('client') === 'desktop';
const owner = params.get('role') !== 'member';
const asset = '../../../internal/adapters/web/';
const logo = asset + 'brand/icon-192.png';
const landscape = asset + 'preview-landscape.svg';
const t = s => (english ? s.split('|')[1] : s.split('|')[0]) ?? s.split('|')[0];
const esc = value => String(value).replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
const icon = n => `<svg aria-hidden="true" class="icon"><use href="#i-${n}"></use></svg>`;
const title = {
 room:'Informações da sala|Room info', settings:'Configurações|Settings', engine:'Motor|Engine',
 device:'Este dispositivo|This device', pref:'Preferências da conversa|Conversation preferences',
 media:'Mídia|Media', documents:'Documentos|Documents', links:'Links', stars:'Com estrela|Starred',
 receipts:'Confirmações de leitura|Read receipts', retention:'Retenção de mensagens|Message retention',
 participants:'Participantes|Participants', person:'Perfil|Profile', manage:'Gerenciar sala|Manage room',
 edit:'Editar sala|Edit room', review:'Revisar alterações|Review changes', connection:'Conexão e informações|Connection and information',
 addaccount:'Adicionar conta|Add account', accounts:'Contas|Accounts', account:'Conta|Account', schedules:'Agendadas|Scheduled', revisions:'Histórico de edições|Edit history',
 search:'Buscar mensagens|Search messages', about:'Sobre o Oiê|About Oiê', signout:'Sair|Sign out',
 keys:'Chaves de acesso|Passkeys', diagnostics:'Diagnóstico|Diagnostics', emoji:'Idiomas dos atalhos de emoji|Emoji shortcut languages',
 notifications:'Notificações|Notifications', storage:'Armazenamento|Storage', display:'Mídia e exibição|Media and display',
 access:'Modo do motor|Engine mode', danger:'Ações destrutivas|Destructive actions', bans:'Banimentos|Bans',
 viewer:'Arquivo|File', signedout:'Sessão encerrada|Signed out', dminfo:'Informações da conversa|Conversation info'
};
const people = {ana:'Ana Luz',gui:'Guilherme',ivo:'Ivo Silva'};
const state = {
 name:'Oiê XMPP Client', topic:null, chat:'room', person:'ana', appearance:'dark', zone:'system',
 emojiPT:true, emojiEN:true, receipts:'inherit', retention:'inherit', days:180,
 globalReceipts:'send', globalRoom:'180', globalDirect:'unlimited', quiet:false, offline:false,
 archive:false, markdown:true, autoImages:true, autoVideo:true, quality:'SD', quota:'1 MiB', alerts:true,
 slug:'land', signed:false, engineMode:'local', engineURL:'https://oie.example.test'
};
const topic = () => state.topic ?? t('Um espaço para conversar sobre o Oiê.|A place to talk about Oiê.');
const accounts=[{name:'Guilherme',address:'gui@example.test',connected:true}];
let activeAccount=accounts[0];
const prefs = {room:{},direct:{}};
const prefValue = key => prefs[state.chat][key] ?? state[key];
const roomChat = () => state.chat === 'room';
const chatPage = () => roomChat() ? 'room' : 'dminfo';
const chatName = () => roomChat() ? state.name : people.ana;
const inboxDraft = {room:'',direct:''}, sent = {room:[],direct:[]};
const shared = {media:{q:'',retracted:false},links:{q:'',retracted:false},documents:{q:'',retracted:false}};
let queryPeople='', queryMessages='', draft=null, current='', statusTimer;
const historyFrames = new Map();
const parents = {engine:'settings',device:'settings',emoji:'settings',keys:'settings',about:'settings',diagnostics:'settings',danger:'settings',signout:'settings',storage:'device',notifications:'device',display:'device',access:'engine',edit:'manage',review:'edit',manage:'room',bans:'manage',person:'participants',participants:'room',connection:'room',pref:'room',media:'room',documents:'room',links:'room',stars:'room',retention:'pref',receipts:'pref',viewer:'media',account:'accounts',addaccount:'accounts',search:'room'};
const nameOf = key => t(title[key] ?? key);
const avatar = (id,content='') => `<span aria-hidden="true" class="avatar ${id}">${id==='room'?`<img src="${logo}" alt="">`:esc(content || people[id]?.split(' ').map(x=>x[0]).join('') || 'GC')}</span>`;
const goButton = (dest,label,ic,cls='iconbtn') => `<a href="#${dest}" data-go="${dest}" class="${cls}" title="${esc(label)}" aria-label="${esc(label)}">${icon(ic)}</a>`;
const action = (act,txt,primary=false,extra='') => `<button type="button" class="action ${primary?'primary':''}" data-act="${act}" ${extra}>${esc(t(txt))}</button>`;
const row = (dest,ic,txt=title[dest],value='',leading='') => `<a class="row" href="#${dest}" data-go="${dest}">${leading || icon(ic)}<span class="name">${esc(t(txt))}</span>${value ? `<small>${esc(value)}</small>`:''}${icon('chevron-right')}</a>`;
const accountRow = (a,i) => `<a class="row" href="#account" data-go="account" data-account="${i}">${avatar('gui',a.name.slice(0,2).toUpperCase())}<span class="name">${esc(a.name)}</span><small>${a.connected?t('Conectada|Connected'):t('Desconectada|Disconnected')}</small>${icon('chevron-right')}</a>`;
const personRow = id => `<a class="row" href="#person" data-go="person" data-person="${id}">${avatar(id)}<span class="name">${people[id]}</span>${icon('chevron-right')}</a>`;
const group = (label,body) => `<section class="group"><h2>${esc(t(label))}</h2>${body}</section>`;
const facts = entries => `<dl class="facts">${entries.map(([k,v])=>`<div><dt>${esc(t(k))}</dt><dd>${esc(v)}</dd></div>`).join('')}</dl>`;
const hint = s => `<p class="hint">${esc(t(s))}</p>`;
const field = (key,label,options,value,hideLabel=false) => `<label class="field">${hideLabel?'':`<span>${esc(t(label))}</span>`}<select data-key="${key}" aria-label="${esc(t(label))}">${options.map(([id,txt])=>`<option value="${id}" ${String(value)===id?'selected':''}>${esc(t(txt))}</option>`).join('')}</select></label>`;
const check = (key,label,val,isSwitch=false) => `<label class="${isSwitch?'switchline':'checkline'}">${isSwitch?`<span>${esc(t(label))}</span>`:''}<input type="checkbox" data-key="${key}" ${isSwitch?'role="switch"':''} ${val?'checked':''}>${isSwitch?'':esc(t(label))}</label>`;
const searchBox = (id,label,value) => `<input id="${id}" type="search" placeholder="${esc(t(label))}" aria-label="${esc(t(label))}" value="${esc(value)}">`;
const identity = (id,name,detail) => `<div class="hero">${avatar(id)}<div><h2>${esc(name)}</h2><p>${esc(detail)}</p></div></div>`;
function announce(s) {
  clearTimeout(statusTimer);
  $('status').textContent = t(s);
  statusTimer = setTimeout(()=>$('status').textContent='',2300);
}
function renderChat() {
  document.documentElement.lang = english?'en':'pt-BR';
  const settingLink = goButton('settings',nameOf('settings'),'cog-6-tooth');
  const accountLink = goButton('accounts',nameOf('accounts'),'user-circle');
  $('rail').setAttribute('aria-label',t('Navegação|Navigation'));
  $('rail').innerHTML = `<img src="${logo}" alt="Oiê">${goButton(chatPage(),t('Conversa atual|Current chat'),'rectangle-group','active')}${goButton('schedules',nameOf('schedules'),'clock')}<div class="space"></div>${settingLink}${accountLink}`;
  $('inbox').innerHTML = `<h2>${t('Conversas|Chats')}</h2>${['room','direct'].map(id=>`<button type="button" class="inbox-row ${id===state.chat?'selected':''}" data-chat="${id}">${avatar(id==='room'?'room':'ana')}<span><strong>${esc(id==='room'?state.name:people.ana)}</strong><small>${id==='room'?t('Ficou ótima!|Looks great!'):t('Obrigada pela imagem.|Thanks for the image.')}</small></span></button>`).join('')}`;
  const bubble = (id,txt,time,picture=false,history=false) => `<div class="message ${id==='self'?'own':''}"><div class="bubble">${id==='self'?'':`<a href="#person" data-go="person" data-person="${id}">${people[id]}</a>`}${picture?`<a href="#viewer" data-go="viewer" data-file="land"><img class="art" src="${landscape}" alt="${t('Ilustração de montanhas|Mountain illustration')}"></a>`:''}<p>${esc(txt)}</p><time>${history?`<a href="#revisions" data-go="revisions">${t('editada|edited')}</a> · `:''}${time}</time></div></div>`;
  $('chat').innerHTML = `<header class="chathead"><button class="identity-btn" data-go="${chatPage()}">${avatar(roomChat()?'room':'ana')}<span><strong>${esc(chatName())}</strong><small>${roomChat()?t('Sala · 3 participantes|Room · 3 participants'):t('Disponível|Available')}</small></span></button><div class="tools"><span class="phoneonly">${settingLink}${accountLink}</span>${goButton(chatPage(),nameOf(chatPage()),'information-circle')}</div></header>
    <div class="stream"><p class="date">${t('Hoje, 24 de novembro|Today, November 24')}</p>${bubble('ana',t('Separei uma imagem para testar a galeria.|I picked an image to try in the gallery.'),'14:26')}${bubble('ana',t('Serra.svg|Ridge.svg'),'14:27',true)}${bubble('self',t('Ficou ótima!|Looks great!'),'14:29')}${roomChat()?bubble('ivo',t('Deixei o roteiro nos documentos da sala.|I left the outline in the room documents.'),'14:32',false,true):''}${sent[state.chat].map(text=>bubble('self',text,t('agora|now'))).join('')}</div>
    <form id="composer" class="composer"><input type="text" id="draft" value="${esc(inboxDraft[state.chat])}" aria-label="${t('Mensagem|Message')}" placeholder="${t('Escreva uma mensagem…|Write a message…')}"><button class="action" type="submit">${t('Enviar|Send')}</button></form>`;
}
function settingsView(){
  return group('Geral|General',`<div class="choice"><span>${t('Aparência|Appearance')}</span><div class="segment" role="group" aria-label="${t('Aparência|Appearance')}">${[['system','Sistema|System'],['light','Claro|Light'],['dark','Escuro|Dark']].map(([k,s])=>`<button data-theme-choice="${k}" aria-pressed="${state.appearance===k}">${t(s)}</button>`).join('')}</div></div>
  ${field('lang','Idioma|Language',[['pt','🇧🇷 Português do Brasil'],['en','🇺🇸 English']],english?'en':'pt')}
  ${field('zone','Fuso horário de exibição|Display time zone',[['system','Fuso do sistema|System time zone'],['America/Sao_Paulo','São Paulo'],['UTC','UTC']],state.zone)}
  ${row('emoji','squares-2x2',title.emoji,[state.emojiPT?'PT':'',state.emojiEN?'EN':''].filter(Boolean).join(' · ')||t('Desativados|Off'))}`)+
  group('Dispositivo e motor|Device and engine',row('device','window')+row('engine','squares-2x2',title.engine,desktop?t('Desktop|Desktop'):t('Hospedado|Hosted')))+
  group('Segurança e suporte|Security and support',(!desktop?row('keys','lock-closed'):'')+row('diagnostics','shield-check')+row('about','information-circle')+row('danger','shield-check'));
}
function roomView() {
  const summary = roomChat()?t('Sala · 3 participantes|Room · 3 participants'):t('Disponível|Available');
  const related = row('media','rectangle-group')+row('links','link')+row('documents','document-text')+row('stars','star');
  return identity(roomChat()?'room':'ana',chatName(),summary)+
    (roomChat()&&owner?row('manage','pencil-square'):'')+
    (roomChat()?`<p class="topic">${esc(topic())}</p>`:'')+
    group('Conteúdo da conversa|Conversation content',related)+
    group(roomChat()?'Sala|Room':'Conversa|Conversation',(roomChat()?row('participants','users',title.participants,'3'):'')+row('pref','cog-6-tooth')+row('connection','information-circle'))+
    group('Mensagens|Messages',row('search','magnifying-glass')+row('schedules','clock'));
}
const receiptLabel = mode => mode==='send'?t('Enviar|Send'):t('Não enviar|Do not send');
const periodLabel = val => val==='unlimited'?t('Sem limite|Unlimited'):`${val} ${t('dias|days')}`;
function prefView(){
  const r = prefValue('receipts'), d = prefValue('retention');
  return group('Privacidade e histórico|Privacy and history',row('receipts','check',title.receipts,r==='inherit'?t('Padrão|Default'):receiptLabel(r))+
    row('retention','clock',title.retention,d==='inherit'?t('Padrão|Default'):periodLabel(d==='custom'?prefValue('days'):d)))+
    group('Neste dispositivo|On this device',check('quiet','Silenciar notificações|Mute notifications',prefValue('quiet'),true)+check('offline','Manter conversa offline|Keep chat offline',prefValue('offline'),true))+
    (roomChat()?group('Histórico da sala|Room history',check('archive','Recuperar histórico automaticamente|Automatically retrieve history',prefValue('archive'),true)):'');
}
const defaultPeriod = () => roomChat()?state.globalRoom:state.globalDirect;
function policyView(){
  const item=writes.get(state.chat+'-'+current);
  const choice=key=>(item?.pending||item?.failed ? item.values[key] : undefined) ?? prefValue(key);
  if(current==='receipts') return field('receipts',title.receipts,[['inherit',t('Usar padrão|Use default')+` (${receiptLabel(state.globalReceipts)})`],['send','Enviar|Send'],['off','Não enviar|Do not send']],choice('receipts'),true);
  const mode = choice('retention');
  return `<p class="notice">${t('Encurtar o prazo pode remover mensagens antigas deste histórico.|A shorter period can remove older messages from this history.')}</p>`+
    field('retention',title.retention,[['inherit',t('Usar padrão|Use default')+` (${periodLabel(defaultPeriod())})`],['30','30 dias|30 days'],['180','180 dias|180 days'],['unlimited','Sem limite|Unlimited'],['custom','Definir um prazo|Set a period']],mode,true)+
    `<label class="field" id="custom-field" ${mode==='custom'?'':'hidden'}><span>${t('Dias|Days')}</span><input id="custom-days" type="number" min="1" max="365000" required step="1" value="${mode==='custom'?choice('days'):''}"></label>`;
}


const files = [
  {id:'land',cat:'media',label:'Serra.svg|Ridge.svg',time:'14:27',image:landscape},
  {id:'tone',cat:'media',label:'Teste de som.mp3|Sound check.mp3',time:'14:24',duration:'0:01',audio:'../../../tests/browser/fixtures/covered-tone.mp3'},
  {id:'brand',cat:'media',label:'Ícone do Oiê.png|Oiê icon.png',time:'14:20',image:logo},
  {id:'ping',cat:'media',label:'Notificação.wav|Notification.wav',time:'14:15',duration:'<1 s',audio:asset+'notification.wav'},
  {id:'old',cat:'media',label:'Primeira versão.svg|First version.svg',time:'14:02',image:landscape,retracted:true},
  {id:'notes',cat:'documents',label:'Roteiro da conversa.txt|Conversation outline.txt',time:'14:32',text:'Revisar a galeria, ouvir o som e escolher o ícone da sala.|Review the gallery, listen to the sound and choose the room icon.'},
  {id:'oldnotes',cat:'documents',label:'Rascunho.txt|Draft.txt',time:'14:10',text:'Rascunho do roteiro.|Draft outline.',retracted:true},
  {id:'site',cat:'links',label:'Oiê · XMPP',time:'14:12',text:'https://github.com/araihu/oie'}
];
const isShared = id => Object.hasOwn(shared,id);
const fileMeta = f => f.time+(f.retracted?' · '+t('Retraída|Retracted'):'');
function results() {
  const filter = shared[current];
  const found = files.filter(f=>f.cat===current && (!f.retracted || filter.retracted) && t(f.label).toLocaleLowerCase().includes(filter.q.toLocaleLowerCase().trim()));
  if(!found.length) return `<p class="empty" role="status">${t('Nenhum resultado. Tente outra busca.|No results. Try another search.')}</p>`;
  const section = `<h2 class="dateheading">${t('24 de novembro|November 24')}</h2>`;
  if(current!=='media') return section+found.map(f=>`<a class="row" href="#viewer" data-go="viewer" data-file="${f.id}">${icon(current==='links'?'link':'document-text')}<span class="name">${t(f.label)}</span><small>${fileMeta(f)}</small>${icon('chevron-right')}</a>`).join('');
  return section+`<div class="media-grid">${found.map(f=>`<figure class="tile">${f.audio?`<button class="audioctl" data-audio="${f.id}" aria-label="${esc(t('Reproduzir|Play')+' '+t(f.label))}">${icon('play')}<span class="wave" aria-hidden="true">${[5,9,6,16,11,20,15,10,7,18,13,9,16,19,8,5,7,10,4,6].map(h=>`<i style="height:${h}px"></i>`).join('')}</span><span class="duration">${esc(f.duration)}</span></button>`:`<a href="#viewer" data-go="viewer" data-file="${f.id}"><img class="${f.id==='brand'?'brandart':''}" src="${f.image}" alt="${esc(t(f.label))}"></a>`}<figcaption>${t(f.label)}<br><small>${fileMeta(f)}</small></figcaption></figure>`).join('')}</div>`;
}
const observer = new ResizeObserver(entries => {
  entries.forEach(({target})=>{if(target.isConnected) target.style.gridRowEnd='span '+Math.ceil(target.getBoundingClientRect().height+10)});
});
function measureMedia(){observer.disconnect();document.querySelectorAll('.tile').forEach(tile=>observer.observe(tile));}
function gallery(){
  const f=shared[current];
  return `<nav class="segment" aria-label="${t('Compartilhados|Shared content')}">${Object.keys(shared).map(k=>`<a href="#${k}" data-go="${k}" ${k===current?'aria-current="page"':''}>${nameOf(k)}</a>`).join('')}</nav><div class="searchbar">${searchBox('file-search','Buscar arquivos e links|Search files and links',f.q)}${check('retracted','Incluir retraídas|Include retracted',f.retracted)}</div><div id="results">${results()}</div>`;
}
function filteredPeople(){
  const match=id=>people[id].toLocaleLowerCase().includes(queryPeople.toLocaleLowerCase());
  const blocks=[['Proprietário|Owner',['gui']],['Participantes|Participants',['ana','ivo']]].map(([k,ids])=>{
    const show=ids.filter(match);return show.length?group(k,show.map(personRow).join('')):'';
  });
  return blocks.join('') || hint('Nenhum participante encontrado.|No matching participants.');
}
function editView(){
  return `<form id="room-form"><label class="field"><span>${t('Nome da sala|Room name')}</span><input type="text" id="room-name" required maxlength="100" value="${esc(draft.name)}"></label><label class="field"><span>${t('Tópico|Topic')}</span><textarea id="room-topic" maxlength="2000">${esc(draft.topic)}</textarea></label></form>`;
}
function reviewView(){
  return hint('Confira o que os participantes verão na sala.|Review what participants will see in the room.')+
    [[t('Nome da sala|Room name'),state.name,draft.name],[t('Tópico|Topic'),topic(),draft.topic]].filter(([,a,b])=>a!==b).map(([label,a,b])=>`<div class="reviewchange"><strong>${label}</strong><del>${esc(a||t('Sem tópico|No topic'))}</del><ins>${esc(b||t('Sem tópico|No topic'))}</ins></div>`).join('');
}
function view(){
  if(isShared(current)) return gallery();
  const example = id=>files.find(f=>f.id===id);
  const days = [['30','30 dias|30 days'],['180','180 dias|180 days'],['unlimited','Sem limite|Unlimited']];
  switch(current){
    case 'room': case 'dminfo': return roomView();
    case 'settings': return settingsView();
    case 'pref': return prefView();
    case 'receipts': case 'retention': return policyView();
    case 'engine': return (desktop?group('Conexão|Connection',row('access','window',title.access,state.engineMode==='local'?'Local':t('Remoto|Remote'))):'')+
      group('Padrões do motor|Engine defaults',hint('As conversas podem usar um prazo e uma confirmação de leitura próprios.|Each conversation can override these defaults.')+
      field('globalReceipts',title.receipts,[['send','Enviar|Send'],['off','Não enviar|Do not send']],state.globalReceipts)+
      hint('Um prazo menor pode remover mensagens antigas.|A shorter period can remove older messages.')+
      field('globalRoom','Histórico de salas|Room history',days,state.globalRoom)+field('globalDirect','Histórico de conversas diretas|Direct chat history',days,state.globalDirect));
    case 'access': return `<form id="mode-form">${field('modeDraft',title.access,[['local','Local'],['remote','Remoto|Remote']],state.engineMode)}
      <div id="remote-fields" ${state.engineMode==='local'?'hidden':''}>
        <label class="field"><span>${t('Endereço do motor|Engine address')}</span><input type="url" id="mode-url" ${state.engineMode==='local'?'disabled':''} required value="${esc(state.engineURL)}"></label>
        <label class="field"><span>${t('Chave de acesso|Access key')}</span><input type="password" id="mode-key" readonly value="fixture"></label>
      </div></form>`;
    case 'device': return row('display','rectangle-group')+row('notifications','bell')+row('storage','folder');
    case 'display': return field('quality','Qualidade de envio|Upload quality',[['SD','SD'],['HD','HD'],['OG','Original']],state.quality)+check('markdown','Exibir Markdown|Render Markdown',state.markdown,true)+check('autoImages','Baixar imagens automaticamente|Automatically download images',state.autoImages,true)+check('autoVideo','Carregar miniaturas de vídeo|Load video thumbnails',state.autoVideo,true);
    case 'notifications': return check('alerts','Exibir notificações|Show notifications',state.alerts,true);
    case 'storage': return field('quota','Limite do histórico offline|Offline history limit',['256 KiB','512 KiB','1 MiB','2 MiB'].map(v=>[v,v]),state.quota);
    case 'emoji': return check('emojiPT','Português do Brasil',state.emojiPT)+check('emojiEN','English',state.emojiEN);
    case 'connection': return facts([['Conta|Account','gui@example.test'],['Conexão da conta|Account connection',accounts[0].connected?t('Conectada|Connected'):t('Desconectada|Disconnected')],...(roomChat()?[['Endereço da sala|Room address','oie@salas.example.test'],['Sessão na sala|Room session',t('Na sala|Joined')]]:[['Contato|Contact','ana@example.test'],['Presença|Presence',t('Disponível|Available')]])]);
    case 'participants': return searchBox('people-search','Buscar participante|Search participants',queryPeople)+`<div id="people-list">${filteredPeople()}</div>`;
    case 'person': return identity(state.person,people[state.person],roomChat()?state.name:'ana@example.test')+facts(roomChat()?[['Papel na sala|Room role',state.person==='gui'?t('Proprietário|Owner'):t('Participante|Participant')]]:[['Presença|Presence',t('Disponível|Available')]]);
    case 'manage': return `<a class="action" href="#edit" data-go="edit">${icon('pencil-square')}${t('Editar nome e tópico|Edit name and topic')}</a>`+
      facts([['Nome da sala|Room name',state.name],['Tópico|Topic',topic() || t('Sem tópico|No topic')]])+
      group('Pessoas|People',row('participants','users')+row('bans','shield-check'));
    case 'edit': return editView();
    case 'review': return reviewView();
    case 'bans': return hint('Nenhum endereço banido.|No banned addresses.');
    case 'stars': return `<a class="row" href="#viewer" data-go="viewer" data-file="land">${icon('star')}<span class="name">${t(example('land').label)}</span><small>Ana · 14:27</small>${icon('chevron-right')}</a>`;
    case 'viewer': {
      const f=example(state.slug);return `<h2>${esc(t(f.label))}</h2><p class="hint">${fileMeta(f)}</p>`+(f.image?`<img class="viewer" src="${f.image}" alt="${esc(t(f.label))}">`:f.cat==='links'?`<a href="${f.text}" target="_blank" rel="noopener">${esc(f.text)} ${icon('arrow-top-right-on-square')}</a>`:`<p>${esc(t(f.text))}</p>`);
    }
    case 'search':return searchBox('msg-search','Buscar nesta conversa|Search this conversation',queryMessages)+`<div id="msg-results">${messageResults()}</div>`;
    case 'schedules':return hint('Nenhuma mensagem agendada nesta conversa.|No scheduled messages in this chat.');
    case 'revisions':return facts([['14:32 · '+t('Atual|Current'),t('Deixei o roteiro nos documentos da sala.|I left the outline in the room documents.')],['14:31',t('Deixei o roteiro.|I left the outline.')]]);
    case 'accounts':return accounts.map(accountRow).join('');
    case 'addaccount':return `<form id="add-account"><label class="field"><span>${t('Nome de exibição|Display name')}</span><input type="text" name="displayName" required maxlength="100"></label><label class="field"><span>${t('Endereço XMPP|XMPP address')}</span><input type="email" name="address" placeholder="nome@example.test" required></label></form>`;
    case 'account':return identity('gui',activeAccount.name,activeAccount.address)+facts([['Conexão|Connection',activeAccount.connected?t('Conectada|Connected'):t('Desconectada|Disconnected')]]);
    case 'keys':return facts([['Chave de acesso|Passkey',t('Dispositivo pessoal|Personal device')]]);
    case 'diagnostics':return facts([['Motor|Engine',desktop?t('Local|Local'):t('Hospedado|Hosted')],['Conexão|Connection',t('Disponível|Available')]]);
    case 'danger':return hint('A exclusão de dados está indisponível nesta sessão.|Data deletion is unavailable in this session.');
    case 'about':return identity('room','Oiê',t('Cliente XMPP|XMPP client'));
    case 'signout':return hint('Encerrar a sessão do Oiê neste navegador?|Sign out of Oiê in this browser?');
    case 'signedout':return hint('Você saiu desta sessão de exemplo.|You signed out of this example session.');
  }
}
function footer(){
  switch(current){
    case 'settings':return desktop?'':`<a class="action" href="#signout" data-go="signout">${nameOf('signout')}</a>`;
    case 'signout':return action('back','Cancelar|Cancel')+action('signout','Sair|Sign out',true);
    case 'edit':return action('cancel-edit','Cancelar|Cancel')+`<button type="submit" form="room-form" class="action primary" id="review-button">${t('Revisar alterações|Review changes')}</button>`;
    case 'review':return action('back','Continuar editando|Keep editing')+action('publish','Publicar alterações|Publish changes',true);
    case 'access':return `<button class="action primary" type="submit" form="mode-form">${t('Usar este motor|Use this engine')}</button>`;
    case 'accounts':return `<a class="action" href="#addaccount" data-go="addaccount">${nameOf('addaccount')}</a>`;
    case 'addaccount':return `<button type="submit" form="add-account" class="action primary">${nameOf('addaccount')}</button>`;
    case 'account':return action('account-connect',activeAccount.connected?'Desconectar|Disconnect':'Conectar|Connect');
    case 'signedout':return action('reset','Reabrir exemplo|Reopen example');
    default:return '';
  }
}
function messageResults(){
  const needle=queryMessages.toLocaleLowerCase().trim();
  if(!needle)return '';
  const texts=[...document.querySelectorAll('.bubble p')].map(p=>p.textContent).filter(s=>s.toLocaleLowerCase().includes(needle));
  return texts.length?facts(texts.map(v=>[chatName(),v])):hint('Nenhuma mensagem encontrada.|No matching messages.');
}


const phone = matchMedia('(max-width:740px)');
const systemDark = matchMedia('(prefers-color-scheme:dark)');
let seq = 0, frame = {id:0,path:[],origin:''};
let audio, audioButton;
let failRead = params.get('state')==='error';
let delayRead = params.get('state')==='loading';
let firstWriteFails = params.get('save')==='error';
const writes = new Map();
function policyStatus(){
  return '<div id="policy-feedback" role="status"></div>';
}
function renderPolicyStatus(){
  const region=$('policy-feedback');
  if(!region)return;
  const item=writes.get(state.chat+'-'+current);
  region.innerHTML=item?.pending?hint('Salvando…|Saving…'):item?.failed?
    hint('A preferência anterior continua em uso.|Your previous preference is still in use.')+action('retry-save','Tentar novamente|Try again'):item?.saved?hint('Preferência atualizada.|Preference updated.'):'';
}
function savePolicy(key,values){
  const scope=state.chat, id=scope+'-'+key;
  const item={values,pending:true};writes.set(id,item);renderPolicyStatus();
  setTimeout(()=>{
    if(writes.get(id)!==item)return;
    item.pending=false;
    if(firstWriteFails){firstWriteFails=false;item.failed=true;}
    else {Object.assign(prefs[scope],values);item.saved=true;}
    if(scope===state.chat&&current===key)renderPolicyStatus();
  },180);
}
function stopAudio(){
  if(audio)audio.pause();
  if(audioButton){audioButton.querySelector('use').setAttribute('href','#i-play');audioButton.setAttribute('aria-label',t('Reproduzir|Play')+' '+t(files.find(f=>f.id===audioButton.dataset.audio).label));}
  audio=null;audioButton=null;
}
function setTheme(){
  document.documentElement.dataset.theme=state.appearance==='system'?(systemDark.matches?'dark':'light'):state.appearance;
}
function scrollKey(el){
  return el?.id || (el?.dataset.person?'person-'+el.dataset.person:el?.dataset.file?'file-'+el.dataset.file:el?.dataset.go?'go-'+el.dataset.go:'');
}
function findTarget(key,root){
  if(!key)return null;
  if(key.startsWith('go-'))return [...root.querySelectorAll(`[data-go="${CSS.escape(key.slice(3))}"]`)].find(e=>e.getClientRects().length);
  if(key.startsWith('person-'))return root.querySelector(`[data-person="${CSS.escape(key.slice(7))}"]`);
  if(key.startsWith('file-'))return root.querySelector(`[data-file="${CSS.escape(key.slice(5))}"]`);
  return $(key);
}
function modalMode(){
  const lock=phone.matches&&Boolean(current);
  for(const id of ['rail','inbox','chat'])$(id).inert=lock;
  $('drawer').setAttribute('role',lock?'dialog':'complementary');
  if(lock)$('drawer').setAttribute('aria-modal','true');else $('drawer').removeAttribute('aria-modal');
}
function updateReviewButton(){
  const b=$('review-button');if(b)b.disabled=!draft.name.trim()||(draft.name.trim()===state.name&&draft.topic.trim()===topic());
}
function paint(restore=false){
  stopAudio();observer.disconnect();clearTimeout(statusTimer);$('status').textContent='';
  current=frame.path.at(-1)||'';
  if(!owner&&['manage','edit','review','bans'].includes(current)){current='room';frame.path=['room'];}
  if(current==='access'&&!desktop){current='engine';frame.path=['settings','engine'];}
  if(current==='edit'&&!draft)draft={name:state.name,topic:topic()};
  if((current==='review'||current==='edit')&&roomChat()===false){state.chat='room';renderChat();}
  if(current==='review'&&!draft){current='manage';frame.path=['room','manage'];}
  if(state.signed){current='signedout';frame.path=['signedout'];}
  document.body.classList.toggle('session-ended',state.signed);
  if(current!==location.hash.slice(1)&&current){
    history.replaceState({drawerFrame:frame},'','#'+current);
  }
  if(current==='room')state.chat='room';
  if(current==='dminfo')state.chat='direct';
  $('drawer').hidden=!current;modalMode();
  if(!current){(findTarget(frame.origin,document)||document.querySelector('.identity-btn')).focus({preventScroll:true});return;}
  const parent=frame.path.at(-2);
  const sub=parent?nameOf(parent):'';
  $('drawer-head').innerHTML=`${parent?`<button class="iconbtn" data-act="back" aria-label="${esc(t('Voltar para|Back to')+' '+sub)}">${icon('chevron-left')}</button>`:''}<div class="titlebox">${sub?`<div class="context">${esc(sub)}</div>`:''}<h1 id="title" tabindex="-1">${esc(nameOf(current))}</h1></div>${current==='signedout'?'':`<button class="iconbtn" data-act="close" aria-label="${t('Fechar|Close')}">${icon('x-mark')}</button>`}`;
  const failed=failRead&&['connection','participants','media','documents','links'].includes(current);
  const waiting=delayRead&&['participants','connection','media','documents','links'].includes(current);
  if(waiting){
    delayRead=false;
    const taskFrame=frame.id;
    setTimeout(()=>{if(frame.id===taskFrame)paint();},850);
  }
  $('body').innerHTML=waiting?`<p role="status" class="hint">${t('Carregando…|Loading…')}</p><div aria-hidden="true" class="skeleton"><i></i><i></i><i></i></div>`:failed?`<p class="notice" role="alert">${t('Não foi possível carregar estas informações.|These details could not be loaded.')}</p>${action('retry','Tentar novamente|Try again')}`:view();
  if(['receipts','retention'].includes(current)){$('body').insertAdjacentHTML('beforeend',policyStatus());renderPolicyStatus();}
  $('foot').innerHTML=footer();$('foot').hidden=!$('foot').innerHTML;
  updateReviewButton();measureMedia();
  document.title=nameOf(current)+' · Oiê';
  const saved=restore&&historyFrames.get(frame.id);
  $('body').scrollTop=saved?.scroll||0;
  (saved&&findTarget(saved.focus,$('drawer'))||$('title')).focus({preventScroll:true});
}
function remember(trigger){
  historyFrames.set(frame.id,{scroll:$('body').scrollTop,focus:scrollKey(trigger||document.activeElement)});
}
function changeFrame(path,origin=frame.origin,replace=false){
  const parentFrame=!replace && current===path.at(-2) ? frame.id : null;
  frame={id:++seq,path,origin,parentFrame};
  history[replace?'replaceState':'pushState']({drawerFrame:frame},'',`#${path.at(-1)||'chat'}`);
  paint();
}
function openPage(dest,trigger){
  if(!Object.hasOwn(title,dest))return;
  const inside=trigger&&$('drawer').contains(trigger);
  remember(trigger);
  if(trigger?.dataset.account!=null)activeAccount=accounts[Number(trigger.dataset.account)];
  if(trigger?.dataset.person)state.person=trigger.dataset.person;
  if(trigger?.dataset.file)state.slug=trigger.dataset.file;
  if(dest==='edit'&&current!=='review')draft={name:state.name,topic:topic()};
  if(!inside){changeFrame([dest],scrollKey(trigger));return;}
  if(isShared(current)&&isShared(dest)){
    frame={...frame,path:[...frame.path.slice(0,-1),dest]};
    history.replaceState({drawerFrame:frame},'',`#${dest}`);paint();return;
  }
  changeFrame([...frame.path,dest]);
}
function back(){
  if(frame.path.length<2){closeDrawer();return;}
  // Ordinary pushes have a parent frame. Deep links get a synthetic parent.
  if(frame.parentFrame!=null){history.back();return;}
  const child=current;
  changeFrame(frame.path.slice(0,-1),frame.origin,true);
  findTarget('go-'+child,$('drawer'))?.focus({preventScroll:true});
}
function closeDrawer(){remember();changeFrame([],frame.origin||'go-'+chatPage());}
function renderResults(){ $('results').innerHTML=results();measureMedia();}
function confirmDays(el) {
  if(current!=='retention'||$('custom-field').hidden)return;
  if(!el.checkValidity()){el.reportValidity();return;}
  const item=writes.get(state.chat+'-retention');
  if(item?.pending&&item.values.retention==='custom'&&item.values.days===Number(el.value))return;
  if(!item?.pending&&!item?.failed&&prefValue('retention')==='custom'&&prefValue('days')===Number(el.value))return;
  savePolicy('retention',{retention:'custom',days:Number(el.value)});
}
document.addEventListener('click',async e=>{
  const el=e.target.closest('a,button');if(!el)return;
  if(el.matches('a')&&(e.metaKey||e.ctrlKey||e.shiftKey||e.altKey))return;
  if(el.dataset.chat){
    state.chat=el.dataset.chat;renderChat();changeFrame([chatPage()],'go-'+chatPage());return;
  }
  if(el.dataset.go){e.preventDefault();openPage(el.dataset.go,el);return;}
  if(el.dataset.themeChoice){
    state.appearance=el.dataset.themeChoice;setTheme();
    document.querySelectorAll('[data-theme-choice]').forEach(b=>b.setAttribute('aria-pressed',String(b===el)));
    announce('Aparência atualizada|Appearance updated');return;
  }
  if(el.dataset.audio){
    const playing=audioButton===el;stopAudio();if(playing)return;
    const f=files.find(x=>x.id===el.dataset.audio);
    const player=new Audio(f.audio);audio=player;audioButton=el;
    const finished=()=>{if(audio===player)stopAudio()};
    player.onended=finished;
    try{await player.play();if(audio===player){el.querySelector('use').setAttribute('href','#i-pause');el.setAttribute('aria-label',t('Pausar|Pause')+' '+t(f.label));}}
    catch{if(audio===player){finished();announce('Áudio indisponível|Audio unavailable');}}return;
  }
  switch(el.dataset.act){
    case 'back':back();break;
    case 'close':closeDrawer();break;
    case 'retry':failRead=false;paint();break;
    case 'retry-save':{
      const change=writes.get(state.chat+'-'+current);
      if(change?.failed)savePolicy(current,change.values);break;
    }
    case 'cancel-edit':draft=null;back();break;
    case 'publish':
      if(owner&&draft){state.name=draft.name;state.topic=draft.topic;draft=null;renderChat();changeFrame(['room','manage'],frame.origin,true);announce('Alterações publicadas|Changes published');}break;
    case 'account-connect':
      activeAccount.connected=!activeAccount.connected;paint();$('foot').querySelector('button').focus();break;
    case 'signout':state.signed=true;changeFrame(['signedout'],frame.origin,true);break;
    case 'reset':location.href=location.pathname+location.search+'#settings';location.reload();break;
  }
});
document.addEventListener('input',e=>{
  const el=e.target;
  if(el.form?.id==='add-account')el.setCustomValidity('');
  switch(el.id){
    case 'file-search':shared[current].q=el.value;stopAudio();renderResults();break;
    case 'people-search':queryPeople=el.value;$('people-list').innerHTML=filteredPeople();break;
    case 'msg-search':queryMessages=el.value;$('msg-results').innerHTML=messageResults();break;
    case 'draft':inboxDraft[state.chat]=el.value;break;
    case 'room-name':draft.name=el.value;updateReviewButton();break;
    case 'room-topic':draft.topic=el.value;updateReviewButton();break;
  }
});
document.addEventListener('change',e=>{
  const el=e.target, key=el.dataset.key;if(!key)return;
  const value=el.type==='checkbox'?el.checked:el.value;
  if(key==='modeDraft'){
    $('remote-fields').hidden=value==='local';
    $('mode-url').disabled=value==='local';return;
  }
  if(key==='retracted'){shared[current].retracted=value;stopAudio();renderResults();return;}
  if(key==='retention'){
    $('custom-field').hidden=value!=='custom';
    if(value==='custom'){$('custom-days').value=prefValue('retention')==='custom'?prefValue('days'):'';$('custom-days').focus();return;}
  }
  if(['retention','receipts'].includes(key)){savePolicy(key,{[key]:value});return;}
  if(['quiet','offline','archive'].includes(key))prefs[state.chat][key]=value;
  else if(key==='lang'){english=value==='en';renderChat();paint();$('body').querySelector('[data-key=lang]').focus();}
  else state[key]=value;
  announce('Preferência atualizada|Preference updated');
});
document.addEventListener('focusout',e=>{if(e.target.id==='custom-days'&&e.target.value)confirmDays(e.target)});
document.addEventListener('submit',e=>{
  e.preventDefault();
  if(e.target.id==='add-account'){
    const form=e.target, name=form.elements.displayName.value.trim(), address=form.elements.address.value.trim();
    if(!name){form.elements.displayName.focus();return;}
    if(accounts.some(a=>a.address.toLowerCase()===address.toLowerCase())){
      form.elements.address.setCustomValidity(t('Esta conta já foi adicionada.|This account has already been added.'));
      form.elements.address.reportValidity();return;
    }
    accounts.push({name,address,connected:false});changeFrame(['accounts'],frame.origin,true);
    announce('Conta adicionada|Account added');return;
  }
  if(e.target.id==='mode-form'){
    state.engineMode=e.target.querySelector('select').value;
    if(state.engineMode==='remote')state.engineURL=$('mode-url').value;
    announce('Motor selecionado|Engine selected');return;
  }
  if(e.target.id==='room-form'){
    if(!draft.name.trim())return;
    draft.name=draft.name.trim();draft.topic=draft.topic.trim();openPage('review',$('review-button'));return;
  }
  if(e.target.id==='composer'&&inboxDraft[state.chat].trim()){
    sent[state.chat].push(inboxDraft[state.chat].trim());inboxDraft[state.chat]='';renderChat();$('draft').focus();document.querySelector('.stream').scrollTop=document.querySelector('.stream').scrollHeight;
  }
});
document.addEventListener('keydown',e=>{
  if(e.key==='Enter'&&e.target.id==='custom-days'){e.preventDefault();confirmDays(e.target);return;}
  if(e.key==='Escape'&&current&&current!=='signedout'){e.preventDefault();closeDrawer();return;}
  if(e.key!=='Tab'||!current||!phone.matches)return;
  const stops=[...$('drawer').querySelectorAll('button:not([disabled]),a[href],input,select,textarea')].filter(el=>el.getClientRects().length);
  if(!stops.length)return;
  if(e.shiftKey&&[stops[0],$('title')].includes(document.activeElement)){e.preventDefault();stops.at(-1).focus();}
  else if(!e.shiftKey&&document.activeElement===stops.at(-1)){e.preventDefault();stops[0].focus();}
});
function deepPath(dest){
  const path=[];while(dest&&title[dest]){path.unshift(dest);dest=parents[dest];}
  return path;
}
window.addEventListener('popstate',e=>{
  remember();frame=e.state?.drawerFrame??{id:++seq,path:deepPath(location.hash.slice(1)),origin:''};
  paint(true);
});
phone.addEventListener('change',modalMode);
systemDark.addEventListener('change',setTheme);
const start=location.hash.slice(1)||'room';
if(start==='dminfo')state.chat='direct';
frame.path=deepPath(start);frame.origin='go-'+(frame.path[0]||'room');
history.replaceState({drawerFrame:frame},'',`#${start}`);
setTheme();renderChat();paint();

