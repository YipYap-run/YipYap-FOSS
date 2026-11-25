// Apply the saved theme before first paint to prevent a flash. Keep in step
// with src/state/theme-store.mjs. Storage access throws when the browser
// blocks site data, so fall back to the system colour scheme.
(function () {
  var t = null;
  try {
    t = window.localStorage.getItem("yipyap-theme");
  } catch (e) {
    t = null;
  }
  if (t !== "light" && t !== "dark" && t !== "oled") {
    t = "light";
    try {
      if (window.matchMedia && window.matchMedia("(prefers-color-scheme: dark)").matches) {
        t = "dark";
      }
    } catch (e) {
      // Keep light.
    }
  }
  document.documentElement.setAttribute("data-theme", t);
})();
