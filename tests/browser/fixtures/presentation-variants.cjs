function* presentationVariants({ fullMatrix = process.argv.includes('--full-matrix') } = {}) {
  const locales = ['en', 'pt-BR'];
  for (const [widthIndex, width] of [390, 1440].entries()) {
    for (const [themeIndex, theme] of ['goshtoso', 'minimal'].entries()) {
      for (const [modeIndex, mode] of ['light', 'dark'].entries()) {
        // Alternating the locale covers every pair of presentation dimensions.
        const languages = fullMatrix ? locales : [locales[(widthIndex + themeIndex + modeIndex) % 2]];
        for (const locale of languages) yield { width, theme, mode, locale };
      }
    }
  }
}

module.exports = { presentationVariants };
