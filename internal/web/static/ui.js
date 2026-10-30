(function () {
  if (window.__hearthBound) return;
  window.__hearthBound = true;

  document.addEventListener("htmx:configRequest", function (event) {
    var node = document.getElementById("csrf");
    if (node && node.dataset.token) {
      event.detail.headers["X-CSRF-Token"] = node.dataset.token;
    }
  });

  function showUtilityRequestError(event) {
    var target = event.detail.target;
    if (!target || !target.classList.contains("utility-feedback")) return;
    var response = event.detail.xhr && event.detail.xhr.responseText;
    target.textContent = response ? response.trim() : "Could not save this utility setting. Try again.";
  }

  document.addEventListener("htmx:responseError", showUtilityRequestError);
  document.addEventListener("htmx:sendError", showUtilityRequestError);

  function formatCents(cents, symbol) {
    var sign = "";
    if (cents < 0) {
      sign = "-";
      cents = -cents;
    }
    var frac = String(cents % 100);
    if (frac.length < 2) frac = "0" + frac;
    return sign + symbol + Math.floor(cents / 100) + "." + frac;
  }

  function renderSpendingChart() {
    if (window.__hearthChart) {
      window.__hearthChart.destroy();
      window.__hearthChart = null;
    }
    var canvas = document.querySelector(".spending-chart");
    if (!canvas || !canvas.dataset.chart || typeof Chart === "undefined") return;
    var payload;
    try {
      payload = JSON.parse(canvas.dataset.chart);
    } catch (err) {
      return;
    }
    var datasets = (payload.datasets || []).map(function (series) {
      return {
        label: series.label,
        data: series.cents.map(function (cents) { return cents / 100; }),
        cents: series.cents,
        borderColor: series.color,
        backgroundColor: series.color + "2e",
        pointBackgroundColor: series.color,
        pointBorderColor: "#fffaf3",
        pointBorderWidth: 1.5,
        pointRadius: 3.5,
        pointHoverRadius: 6,
        borderWidth: 2,
        fill: true,
        tension: 0,
        order: series.order
      };
    });
    window.__hearthChart = new Chart(canvas, {
      type: "line",
      data: { labels: payload.labels, datasets: datasets },
      options: {
        responsive: true,
        maintainAspectRatio: false,
        interaction: { mode: "index", intersect: false },
        plugins: {
          legend: {
            position: "bottom",
            labels: { color: "#241c16", boxWidth: 10, boxHeight: 10, padding: 16, font: { size: 12 } }
          },
          tooltip: {
            backgroundColor: "#241c16",
            titleColor: "#cbbba8",
            bodyColor: "#f6efe6",
            padding: 10,
            boxPadding: 4,
            callbacks: {
              label: function (item) {
                var cents = item.dataset.cents[item.dataIndex];
                return " " + item.dataset.label + "  " + formatCents(cents, payload.symbol);
              }
            }
          }
        },
        scales: {
          x: {
            grid: { display: false },
            ticks: { color: "#6d5e51", maxRotation: 0, autoSkip: true },
            border: { color: "#ddcfc0" }
          },
          y: {
            beginAtZero: true,
            grid: { color: "#ddcfc0" },
            border: { display: false },
            ticks: {
              color: "#6d5e51",
              callback: function (value) {
                return formatCents(Math.round(value * 100), payload.symbol);
              }
            }
          }
        }
      }
    });
  }

  document.addEventListener("DOMContentLoaded", renderSpendingChart);
  document.addEventListener("htmx:afterSwap", renderSpendingChart);

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
