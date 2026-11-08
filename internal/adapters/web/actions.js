(() => {
 'use strict';
 // Public Dropdown item IDs are application hooks. Escape uses its supported
 // keyboard lifecycle to dismiss and restore focus after native actions.
 document.addEventListener('click',event=>{
  const item=event.target.closest('[id^="copy-message-"],[id^="star-message-"],[id^="open-preview-"],[id^="info-preview-"],[id^="save-preview-"]');
  if(item&&!item.disabled)item.dispatchEvent(new KeyboardEvent('keydown',{key:'Escape',bubbles:true,cancelable:true}));
  if(event.target.closest('[data-message-menu-trigger]'))document.dispatchEvent(new CustomEvent('oie:refresh-request'));
 },true);
 document.addEventListener('keydown',event=>{if(event.key==='Escape')setTimeout(()=>document.dispatchEvent(new CustomEvent('oie:refresh-request')),180);});
})();
