// Apply the saved theme before first paint to prevent a flash.
(function () {
  var t = localStorage.getItem("yipyap-theme");
  if (!t) {
    t = window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light";
  }
  document.documentElement.setAttribute("data-theme", t);
})();
