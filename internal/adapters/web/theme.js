(() => {
  const root = document.documentElement;
  const system = window.matchMedia("(prefers-color-scheme: dark)");
  const apply = () => root.classList.toggle("dark",
    root.dataset.mode === "dark" || (root.dataset.mode === "system" && system.matches));
  apply();
  system.addEventListener("change", apply);
})();
