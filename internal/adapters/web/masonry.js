window.OieRuntime.register('masonry', life => {
  'use strict';
  if (typeof ResizeObserver !== 'function') return;

  const selector = '[data-masonry-grid]';
  const scroller = '.oie-shared-scroll';
  const observed = new Set();
  const cardSelector = '[data-favorite-source],[data-shared-item]';

  function measure(grid) {
    // Read the responsive column count and intrinsic sizes in normal grid flow.
    // Old column assignments could otherwise create implicit columns on resize.
    grid.removeAttribute('data-masonry');
    const items = [...grid.children].filter(item => !item.hidden);
    if (!items.some(item => item.matches(cardSelector))) return;
    const style = getComputedStyle(grid);
    const columns = style.gridTemplateColumns.split(' ').length;
    const gap = parseFloat(style.columnGap) || 0;
    const heights = items.map(item => item.offsetHeight);
    const bottoms = Array(columns).fill(0);

    // Place cards in one grid row, so long galleries never exhaust CSS tracks.
    items.forEach((item, index) => {
      const card = item.matches(cardSelector);
      const top = card ? Math.min(...bottoms) : Math.max(...bottoms);
      const bottom = top + heights[index] + gap;
      if (card) {
        const column = bottoms.indexOf(top);
        item.style.setProperty('--masonry-column', String(column + 1));
        bottoms[column] = bottom;
      } else {
        bottoms.fill(bottom);
      }
      item.style.setProperty('--masonry-top', top + 'px');
    });
    grid.setAttribute('data-masonry', '');
    // The scrollport changes width when the sidebar opens. Observing the
    // grid's height here would feed our own placement back into the observer.
    for (const node of [grid.closest(scroller), ...items]) {
      if (!node || observed.has(node)) continue;
      sizes.observe(node, {box: 'border-box'});
      observed.add(node);
    }
  }

  const sizes = new ResizeObserver(entries => {
    const grids = new Set();
    for (const {target} of entries) {
      if (!target.isConnected) continue;
      if (target.matches(scroller)) {
        for (const grid of target.querySelectorAll(selector)) grids.add(grid);
      } else if (target.parentElement?.matches(selector)) {
        grids.add(target.parentElement);
      }
    }
    for (const grid of grids) measure(grid);
  });

  window.OieMasonry = {
    layout(root) {
      for (const item of observed) {
        if (item.isConnected && (item.matches(scroller) || item.parentElement?.matches(selector))) continue;
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
