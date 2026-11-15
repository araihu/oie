(() => {
  'use strict';

  const viewport = window.visualViewport;
  if (!viewport) return;

  const root = document.documentElement;
  const touch = matchMedia('(pointer: coarse)');
  const editable = 'input:not([readonly], [disabled], [type=checkbox], [type=radio], [type=range], [type=file], [type=button], [type=submit]), textarea:not([readonly], [disabled]), [contenteditable=true]';
  let frame = 0;

  // These coordinates follow the browser's visible area without changing its scale.
  function update() {
    frame = 0;
    const {offsetLeft, offsetTop, width, height, scale} = viewport;
    const layoutWidth = root.clientWidth;
    const unzoomedTouch = touch.matches && Math.abs(scale - 1) < 0.01;
    const keyboard = unzoomedTouch && window.innerHeight - height > 80 &&
      (document.activeElement?.matches(editable) || root.hasAttribute('data-oie-keyboard'));

    root.toggleAttribute('data-oie-keyboard', keyboard);
    root.toggleAttribute('data-oie-short-viewport', height <= 540 && layoutWidth <= 1024);
    const bounds = touch.matches ? [
      Math.max(0, offsetLeft), Math.max(0, offsetTop),
      Math.max(0, layoutWidth - width - offsetLeft),
      Math.max(0, window.innerHeight - height - offsetTop), width, height,
    ] : [0, 0, 0, 0, layoutWidth, window.innerHeight];
    const setLength = (key, length) => {
      const value = length.toFixed(2) + 'px';
      if (root.style.getPropertyValue(key) !== value) root.style.setProperty(key, value);
    };
    ['left', 'top', 'right', 'bottom', 'width', 'height'].forEach((side, index) => {
      setLength('--oie-visible-' + side, bounds[index]);
    });
    if (unzoomedTouch) setLength('--oie-workspace-height', height);
    else root.style.removeProperty('--oie-workspace-height');
  }

  function schedule() {
    if (!frame) frame = requestAnimationFrame(update);
  }

  viewport.addEventListener('resize', schedule);
  viewport.addEventListener('scroll', schedule);
  window.addEventListener('resize', schedule);
  window.addEventListener('pageshow', schedule);
  document.addEventListener('focusin', schedule);
  document.addEventListener('focusout', schedule);
  touch.addEventListener('change', schedule);
  update();
})();
