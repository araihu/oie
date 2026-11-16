async function resizeVisibleViewport(page, size) {
  await page.setViewportSize(size);
  // The app applies its visual viewport bounds on the next animation frame.
  await page.waitForFunction(height => {
    const visibleHeight = document.documentElement.style.getPropertyValue('--oie-visible-height');
    return Number.parseFloat(visibleHeight) === height;
  }, size.height);
}

module.exports = { resizeVisibleViewport };
