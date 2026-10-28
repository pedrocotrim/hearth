(function () {
  if (window.__hearthBound) return;
  window.__hearthBound = true;

  document.addEventListener("htmx:configRequest", function (event) {
    var node = document.getElementById("csrf");
    if (node && node.dataset.token) {
      event.detail.headers["X-CSRF-Token"] = node.dataset.token;
    }
  });

  document.addEventListener("click", function (event) {
    var button = event.target.closest("[data-copy]");
    if (!button) return;
    var source = document.getElementById(button.getAttribute("data-copy"));
    if (!source) return;
    var text = source.value || source.textContent || "";
    if (!navigator.clipboard) return;
    navigator.clipboard.writeText(text).then(function () {
      var previous = button.textContent;
      button.textContent = "Copied";
      window.setTimeout(function () {
        button.textContent = previous;
      }, 1400);
    });
  });
})();
