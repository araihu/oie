(() => {
  "use strict";

  const root = document.documentElement;
  const storageKey = "oie.navigation.sidebar";
  const defaultWidth = 208;
  let preferredWidth = defaultWidth;
  let collapsed = false;
  try {
    const saved = JSON.parse(localStorage.getItem(storageKey));
    if (Number.isFinite(saved?.width) && saved.width > 0) preferredWidth = saved.width;
    collapsed = saved?.collapsed === true;
  } catch {}

  function applyLayout() {
    root.toggleAttribute("data-sidebar-collapsed", collapsed);
    root.style.setProperty("--oie-sidebar-width", preferredWidth + "px");
  }

  function save() {
    try {
      localStorage.setItem(storageKey, JSON.stringify({width: preferredWidth, collapsed}));
    } catch {}
  }

  // Apply the saved layout before the shell paints, including on full reload.
  applyLayout();

  window.OieRuntime.register("sidebar", life => {
    const sidebar = document.getElementById("desktop-navigation");
    const toggle = document.querySelector("[data-sidebar-toggle]");
    const splitter = sidebar?.querySelector("[data-sidebar-splitter]");
    if (!toggle || !splitter) return;

    const styles = getComputedStyle(sidebar);
    const min = Number(styles.getPropertyValue("--oie-sidebar-min"));
    const max = Number(styles.getPropertyValue("--oie-sidebar-max"));
    const share = Number(styles.getPropertyValue("--oie-sidebar-viewport-share")) / 100;
    const maxWidth = () => Math.max(min, Math.min(max, Math.floor(window.innerWidth * share)));
    const visibleWidth = () => Math.min(maxWidth(), Math.max(min, Math.round(preferredWidth)));
    const narrow = matchMedia("(max-width: 720px)");
    let drag;

    function updateRange() {
      splitter.setAttribute("aria-valuemin", min);
      splitter.setAttribute("aria-valuemax", maxWidth());
      splitter.setAttribute("aria-valuenow", visibleWidth());
    }

    function updateToggle() {
      const label = collapsed ? toggle.dataset.expandLabel : toggle.dataset.collapseLabel;
      toggle.setAttribute("aria-expanded", String(!collapsed));
      toggle.setAttribute("aria-label", label);
      toggle.title = label;
    }

    function resize(width) {
      preferredWidth = Math.max(min, Math.min(maxWidth(), Math.round(width)));
      applyLayout();
      updateRange();
    }

    function finishDrag() {
      if (!drag) return;
      const {id, startWidth} = drag;
      drag = undefined;
      if (splitter.hasPointerCapture(id)) splitter.releasePointerCapture(id);
      splitter.removeAttribute("data-resizing");
      root.removeAttribute("data-sidebar-resizing");
      if (preferredWidth !== startWidth) save();
    }

    function toggleSidebar() {
      finishDrag();
      collapsed = !collapsed;
      applyLayout();
      updateToggle();
      save();
    }

    life.on(toggle, "click", toggleSidebar);
    life.on(splitter, "pointerdown", event => {
      if (event.button !== 0 || !event.isPrimary || drag) return;
      splitter.setPointerCapture(event.pointerId);
      drag = {
        id: event.pointerId,
        startX: event.clientX,
        lastX: event.clientX,
        width: visibleWidth(),
        startWidth: preferredWidth,
      };
      splitter.setAttribute("data-resizing", "");
      root.setAttribute("data-sidebar-resizing", "");
    });
    life.on(splitter, "pointermove", event => {
      if (event.pointerId !== drag?.id || event.clientX === drag.lastX) return;
      drag.lastX = event.clientX;
      resize(drag.width + event.clientX - drag.startX);
    });
    for (const eventType of ["pointerup", "pointercancel", "lostpointercapture"]) {
      life.on(splitter, eventType, event => {
        if (event.pointerId === drag?.id) finishDrag();
      });
    }
    life.on(splitter, "keydown", event => {
      const width = visibleWidth();
      switch (event.key) {
        case "ArrowLeft": resize(width - 16); break;
        case "ArrowRight": resize(width + 16); break;
        case "Home": resize(min); break;
        case "End": resize(maxWidth()); break;
        case "Enter":
          toggle.focus();
          toggleSidebar();
          break;
        default: return;
      }
      event.preventDefault();
      if (event.key !== "Enter") save();
    });
    life.on(window, "resize", updateRange);
    life.on(narrow, "change", finishDrag);
    life.cleanup(finishDrag);

    updateToggle();
    updateRange();
    root.setAttribute("data-sidebar-ready", "");
  });
})();
