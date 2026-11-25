window.OieRuntime.register('masonry', life => {
  'use strict';
  if (typeof ResizeObserver !== 'function') return;

  const selector = '[data-masonry-grid]';
  const scroller = '.oie-shared-scroll';
  const observed = new Set();
  const cardSelector = '[data-favorite-source],[data-shared-item]';

  function measure(grid) {
    const items = [...grid.children].filter(item => !item.hidden);
    if (!items.some(item => item.matches(cardSelector))) return {grid};
    const style = getComputedStyle(grid);
    const columns = style.gridTemplateColumns.split(' ').length;
    const gap = parseFloat(style.columnGap) || 0;
    const heights = items.map(item => item.offsetHeight);
    return {grid, items, heights, columns, gap};
  }

  function place({grid, items, heights, columns, gap}) {
    if (!items) {
      grid.removeAttribute('data-masonry');
      return;
    }
    const bottoms = Array(columns).fill(0);

    // Place cards in one grid row, so long galleries never exhaust CSS tracks.
    items.forEach((item, index) => {
      const card = item.matches(cardSelector);
      const top = card ? Math.min(...bottoms) : Math.max(...bottoms);
      const bottom = top + heights[index] + gap;
      if (card) {
        const column = bottoms.indexOf(top);
        setPosition(item, '--masonry-column', String(column + 1));
        bottoms[column] = bottom;
      } else {
        bottoms.fill(bottom);
      }
      setPosition(item, '--masonry-top', top + 'px');
    });
    if (!grid.hasAttribute('data-masonry')) grid.setAttribute('data-masonry', '');
    // The scrollport changes width when the sidebar opens. Observing the
    // grid's height here would feed our own placement back into the observer.
    for (const node of [grid.closest(scroller), ...items]) {
      if (!node || observed.has(node)) continue;
      sizes.observe(node, {box: 'border-box'});
      observed.add(node);
    }
  }

  function setPosition(item, property, value) {
    if (item.style.getPropertyValue(property) !== value) item.style.setProperty(property, value);
  }

  function layoutGrids(grids) {
    // A narrower grid can leave stale placements in implicit columns. CSS
    // gives those tracks zero width, so reset only the affected grids.
    const narrowed = [...grids].filter(grid => grid.hasAttribute('data-masonry') &&
      getComputedStyle(grid).gridTemplateColumns.split(' ').includes('0px'));
    for (const grid of narrowed) grid.removeAttribute('data-masonry');

    const layouts = Array.from(grids, measure);
    for (const layout of layouts) place(layout);
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
    layoutGrids(grids);
  });

  window.OieMasonry = {
    layout(root) {
      for (const item of observed) {
        if (item.isConnected && (item.matches(scroller) || item.parentElement?.matches(selector))) continue;
        sizes.unobserve(item);
        observed.delete(item);
      }
      if (!root) return;
      layoutGrids(root.matches(selector) ? [root] : root.querySelectorAll(selector));
    }
  };
  life.cleanup(() => {
    sizes.disconnect();
    delete window.OieMasonry;
  });
});
