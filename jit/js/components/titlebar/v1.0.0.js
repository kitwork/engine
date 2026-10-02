/* titlebar component @v1.0.0 — the window controls a page has to draw for itself.
 *
 * An app opened with app.desktop({ chrome: "html" }) gets a window with NO frame of its own: no
 * close button, no drag strip, nothing to move it by. The page draws that bar, and this component
 * is the part of it that markup cannot say.
 *
 * Usage — every visible state is declared, the component only answers questions:
 *
 *   <header data-kit-component="titlebar" data-kit-alias="$titlebar" data-kit-drag="true">
 *     <button data-kit-drag="false" data-kit-click="$titlebar.back()"
 *             data-kit-bind:disabled="!canGoBack" aria-label="Go back">…</button>
 *     <button data-kit-drag="false" data-kit-click="$titlebar.toggleMaximize()"
 *             data-kit-bind:title="maximized ? 'Restore window' : 'Maximize window'">
 *       <i data-kit-show="!maximized" class="icon-square"></i>
 *       <i data-kit-show="maximized" class="icon-copy"></i>
 *     </button>
 *   </header>
 *
 * WHAT THIS COMPONENT DOES NOT DO. Dragging the window is the data-kit-drag directive's job (a file
 * of its own, delivered to the page that writes it): `data-kit-drag="true"` marks the strip,
 * `="false"` exempts a control, and a double-click there toggles maximise. None of that needs a
 * component, so none of it is here — this is half the size of the KitJS component it replaces.
 *
 * What is left is three things a page cannot express:
 *
 *   1. toggleMaximize — the window must be ASKED which state it is in before the right command can
 *      be sent. kit.window.isMaximized() is that question.
 *   2. `maximized` — the same answer, kept as state so the button can label and draw itself.
 *   3. canGoBack / canGoForward — read from the Navigation API, which reports them only through an
 *      event. A browser without it leaves both false: a disabled button is a safer wrong answer
 *      than a button that does nothing when pressed.
 *
 * Every command is delegated to a kernel service (kit.navigation, kit.window), so this component
 * owns no transport of its own and does nothing at all on the web, where there is no shell.
 */
var TITLEBAR = Symbol("kit:titlebar");

// A promise that never rejects: a shell that cannot answer must not break a button.
function ask(question, fallback) {
  try {
    return Promise.resolve(question()).catch(function () { return fallback; });
  } catch (error) {
    return Promise.resolve(fallback);
  }
}

function syncMaximized(scope) {
  return ask(function () { return window.kit.window.isMaximized(); }, scope.maximized).then(function (answer) {
    scope.maximized = answer === true;
    return scope.maximized;
  });
}

// The Navigation API is the only way to know whether there is anywhere to go back TO. Where it is
// missing, both stay false and the buttons stay disabled.
function syncNavigation(scope) {
  var navigation = window.navigation;
  var back = false;
  var forward = false;
  if (navigation) {
    try {
      back = navigation.canGoBack === true;
      forward = navigation.canGoForward === true;
    } catch (error) { /* an unsupported or disabled API stays fail-closed */ }
  }
  scope.canGoBack = back;
  scope.canGoForward = forward;
}

var titlebarDef = {
  maximized: false,
  canGoBack: false,
  canGoForward: false,

  back: function () { return window.kit.navigation.back(); },
  forward: function () { return window.kit.navigation.forward(); },
  reload: function () { return window.kit.navigation.reload(); },
  minimize: function () { return window.kit.window.minimize(); },
  close: function () { return window.kit.window.close(); },

  toggleMaximize: function () {
    var scope = this;
    return syncMaximized(scope).then(function (maximized) {
      return ask(function () {
        return maximized ? window.kit.window.restore() : window.kit.window.maximize();
      }, null).then(function () {
        scope.maximized = !maximized;
        return scope.maximized;
      });
    });
  },

  init: function (context) {
    var scope = this;
    this[TITLEBAR] = { context: context };

    syncMaximized(scope);
    syncNavigation(scope);

    // The kernel handles a double-click on the drag strip, so the state has to be re-read AFTER it
    // has acted — otherwise the button would keep the label it had before the window moved.
    context.listen(context.host, "dblclick", function () {
      setTimeout(function () { syncMaximized(scope); }, 0);
    });

    if (window.navigation && typeof window.navigation.addEventListener === "function") {
      context.listen(window.navigation, "currententrychange", function () { syncNavigation(scope); });
    }
  }
};

window.kit.component("titlebar", titlebarDef);
window.kit.component("titlebar@v1.0.0", titlebarDef);
