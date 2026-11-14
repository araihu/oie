window.OieRuntime.register('actions', life => {
 'use strict';
 function initializeSourceMenu(event){
  const trigger=event.target.closest?.('[data-message-menu-trigger]');
  const slot=trigger?.closest('[data-source-menu-deferred]');
  if(!slot||!window.Alpine)return;
  // The same input event continues into the now-initialized Goshtoso menu.
  window.Alpine.mutateDom(()=>{
   window.Alpine.destroyTree(slot);
   slot.removeAttribute('x-ignore');
   slot.removeAttribute('data-source-menu-deferred');
   window.Alpine.initTree(slot);
  });
 }
 life.on(document,'click',initializeSourceMenu,true);
 life.on(document,'keydown',event=>{
  if(['Enter',' ','ArrowDown'].includes(event.key))initializeSourceMenu(event);
 },true);
 // Public Dropdown item IDs are application hooks. Escape uses its supported
 // keyboard lifecycle to dismiss and restore focus after native actions.
 life.on(document,'click',event=>{
  const item=event.target.closest('[id^="copy-message-"],[id^="reply-message-"],[id^="star-message-"],[id^="open-preview-"],[id^="info-preview-"],[id^="save-preview-"],[id^="refresh-preview-"]');
  if(item&&!item.disabled)item.dispatchEvent(new KeyboardEvent('keydown',{key:'Escape',bubbles:true,cancelable:true}));
 },true);
});
