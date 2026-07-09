(function () {
  var id = window.drawingId;
  if (!id) return;

  var mount = document.getElementById("canvas-mount");
  if (!mount) return;

  var saveTimer = null;

  function loadScript(url) {
    return new Promise(function (resolve, reject) {
      if (document.querySelector('script[src="' + url + '"]')) {
        resolve();
        return;
      }
      var s = document.createElement("script");
      s.src = url;
      s.onload = resolve;
      s.onerror = reject;
      document.head.appendChild(s);
    });
  }

  function init() {
    var root = ReactDOM.createRoot(mount);
    root.render(
      React.createElement(window.Excalidraw.Excalidraw, {
        excalidrawAPI: function (api) {
          window.excalidrawAPI = api;
          fetch("/api/draw/" + id + "/data")
            .then(function (r) {
              return r.json();
            })
            .then(function (data) {
              if (data && data.elements) {
                api.updateScene({
                  elements: data.elements,
                  appState: data.appState,
                });
              }
            })
            .catch(function (e) {
              console.error("Load error:", e);
            });
        },
        onChange: function (elements, appState) {
          if (saveTimer) clearTimeout(saveTimer);
          saveTimer = setTimeout(function () {
            fetch("/api/draw/" + id + "/save", {
              method: "POST",
              body: JSON.stringify({ elements: elements, appState: appState }),
            }).catch(function (e) {
              console.error("Save error:", e);
            });
          }, 2000);
        },
        theme: document.documentElement.classList.contains("dark")
          ? "dark"
          : "light",
        viewModeEnabled: false,
        zenModeEnabled: false,
        gridModeEnabled: false,
        name: "Canvas",
      }),
    );
  }

  if (window.Excalidraw && window.React && window.ReactDOM) {
    init();
  } else {
    mount.innerHTML =
      '<div class="flex items-center justify-center h-full"><p class="text-xs font-bold text-[var(--fg-muted)] uppercase tracking-wider">Loading editor...</p></div>';
    Promise.all([
      loadScript("https://unpkg.com/react@18/umd/react.production.min.js"),
      loadScript(
        "https://unpkg.com/react-dom@18/umd/react-dom.production.min.js",
      ),
      loadScript(
        "https://unpkg.com/@excalidraw/excalidraw@0.17.0/dist/excalidraw.production.min.js",
      ),
    ]).then(init);
  }

  // Listen for theme changes
  var observer = new MutationObserver(function () {
    if (window.excalidrawAPI) {
      var isDark = document.documentElement.classList.contains("dark");
      window.excalidrawAPI.updateScene({
        appState: { theme: isDark ? "dark" : "light" },
      });
    }
  });
  observer.observe(document.documentElement, {
    attributes: true,
    attributeFilter: ["class"],
  });
})();
