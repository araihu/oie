window.OieRuntime.register('masonry', life => {
  'use strict';
  if (typeof ResizeObserver !== 'function') return;

  const selector = '[data-masonry-grid]';
  const observed = new Set();
  const dirty = new Set();
  let frame;

  // Measure intrinsic card heights and keep the chronological DOM order.
  // The grid's 1px tracks represent the height plus the gap below each card.
  function measure(grid) {
    const items = [...grid.children].filter(item => !item.hidden);
    const hasCards = items.some(item => item.matches('[data-favorite-source],[data-shared-item]'));
    grid.toggleAttribute('data-masonry', hasCards);
    if (!hasCards) {
      for (const item of items) item.style.removeProperty('grid-row-end');
      return;
    }
    const gap = parseFloat(getComputedStyle(grid).columnGap) || 0;
    const spans = items.map(item => `span ${Math.max(1, Math.ceil(item.offsetHeight + gap))}`);
    items.forEach((item, index) => {
      if (item.style.gridRowEnd !== spans[index]) item.style.gridRowEnd = spans[index];
      if (!observed.has(item)) {
        sizes.observe(item, {box: 'border-box'});
        observed.add(item);
      }
    });
  }

  const sizes = new ResizeObserver(entries => {
    for (const {target} of entries) {
      const grid = target.parentElement;
      if (grid?.matches(selector)) dirty.add(grid);
    }
    if (!frame) frame = life.raf(() => {
      frame = 0;
      for (const grid of dirty) if (grid.isConnected) measure(grid);
      dirty.clear();
    });
  });

  window.OieMasonry = {
    layout(root) {
      for (const item of observed) {
        if (item.isConnected && item.parentElement?.matches(selector)) continue;
        sizes.unobserve(item);
        observed.delete(item);
      }
      if (!root) return;
      if (root.matches(selector)) measure(root);
      else for (const grid of root.querySelectorAll(selector)) measure(grid);
    }
  };
  life.cleanup(() => {
    sizes.disconnect();
    delete window.OieMasonry;
  });
});
