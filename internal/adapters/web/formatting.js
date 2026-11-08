(() => {
  'use strict';
  const key='oie.device.render-markdown.v1';
  let enabled=true,storageDenied=false;
  try {enabled=localStorage.getItem(key)!=='false';}catch{storageDenied=true;}
  document.documentElement.dataset.messageMarkdown=String(enabled);
  function applyPreference() {
    const timeline=document.querySelector('#message-timeline');
    const follow=timeline&&timeline.scrollHeight-timeline.scrollTop-timeline.clientHeight<60;
    const anchor=timeline&&[...timeline.querySelectorAll('[data-message-id]')].find(el=>el.getBoundingClientRect().bottom>=timeline.getBoundingClientRect().top);
    const top=anchor?.getBoundingClientRect().top;
    document.documentElement.dataset.messageMarkdown=String(enabled);
    for(const input of document.querySelectorAll('[data-render-markdown]'))input.checked=enabled;
    if(storageDenied){const warning=document.querySelector('[data-device-preference-warning]');if(warning)warning.hidden=false;}
    requestAnimationFrame(()=>{if(!timeline)return;if(follow)timeline.scrollTop=timeline.scrollHeight;else if(anchor?.isConnected)timeline.scrollTop+=anchor.getBoundingClientRect().top-top;});
  }
  document.addEventListener('DOMContentLoaded',applyPreference);
  document.addEventListener('change',event=>{
    if(!event.target.matches('[data-render-markdown]'))return;
    enabled=event.target.checked;
    try{localStorage.setItem(key,String(enabled));}catch{storageDenied=true;}
    applyPreference();
  });
  addEventListener('storage',event=>{if(event.key===key){enabled=event.newValue!=='false';applyPreference();}});
  function copyFallback(original) {
    const previous=document.activeElement,selection=getSelection(),ranges=[];
    if(selection)for(let i=0;i<selection.rangeCount;i++)ranges.push(selection.getRangeAt(i).cloneRange());
    const field=document.createElement('textarea');field.value=original;field.readOnly=true;field.style.cssText='position:fixed;left:-10000px;top:0';
    document.body.append(field);field.focus({preventScroll:true});field.select();
    // The copy event sets a single MIME type, including on insecure HTTP origins
    // where the async Clipboard API is unavailable.
    const copy=event=>{if(event.clipboardData){event.preventDefault();event.clipboardData.setData('text/plain',original);}};
    field.addEventListener('copy',copy);
    let copied=false;
    try{copied=document.execCommand('copy');}
    finally{
      field.remove();previous?.focus({preventScroll:true});
      if(selection){selection.removeAllRanges();for(const range of ranges)selection.addRange(range);}
    }
    if(!copied)throw new Error('copy_failed');
  }
  document.addEventListener('click',async event=>{
    const button=event.target.closest('[data-copy-original],[id^="copy-message-"]');if(!button)return;
    event.preventDefault();
    const id=button.id?.startsWith('copy-message-')?button.id.slice(13):button.closest('[data-message-id]')?.dataset.messageId;
    const row=id&&document.querySelector(`[data-message-id="${CSS.escape(id)}"][data-message-source]`),labels=document.querySelector('#oie-formatting-copy')?.dataset || {};
    const status=document.querySelector('#oie-copy-status');
    try {
      const original=JSON.parse(row.dataset.messageSource);
      if(typeof original!=='string')throw new Error('copy_source_missing');
      if(navigator.clipboard?.writeText){try{await navigator.clipboard.writeText(original);}catch{copyFallback(original);}}
      else copyFallback(original);
      if(status)status.textContent=labels.copied || '';
      button.dataset.copyState='copied';button.title=labels.copied || '';
      setTimeout(()=>{button.dataset.copyState='';button.title=labels.copy || '';},1800);
    }catch{if(status)status.textContent=labels.failed || '';}
  });
})();
