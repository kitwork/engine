/* network component — whether the page can reach the network, and the moment that changes
 * (KITWORK_UI §13, Kitwork UI's component="network"). The author draws the notice, once, as the
 * toast's (overlay.ui):
 *
 *   <box component="network" as="$network" place="bottom-start">
 *     <card show="open" flex="row" gap="3" padding="3">
 *       <icon show="!connected" value="wifi-off" color="warning+5" alt="Offline" />
 *       <icon show="connected" value="wifi" color="success" alt="Online" />
 *       <text weight="bold">{{ connected ? 'Back online' : 'You are offline' }}</text>
 *       <button click="close()"><icon value="x" alt="Dismiss" /></button>
 *     </card>
 *   </box>
 *
 * The engine writes the host as data-kitwork-component="network: $network", fixed at the place named
 * (classes network network-bottom-start) and a status region a screen reader hears (role="status").
 * Written empty, with only its events, the host is heard and never seen:
 *
 *   <box component="network" offline="$toast.warning('Mất kết nối mạng.')" online="…" />
 *
 *   connected  false while the browser says the device has no network; true on the server's first
 *              paint, corrected as the kernel starts
 *   open       whether the notice shows: from the moment the connection goes until the visitor
 *              closes it, and for duration after it comes back; false on the server's first paint
 *   close()    close the notice until the next change
 *   delay      how long the network must stay gone before it counts as gone, in milliseconds (1000;
 *              the host's delay="1s", read when the page compiles): a shorter break, a change of Wi-Fi,
 *              changes nothing, opens nothing and sends no event. Drive's stay (kitwork:offline) does
 *              not wait: the visitor just tried something and must hear why it did not go
 *   duration   how long "back online" stays before it closes by itself, in milliseconds (4000, as long
 *              as a toast's success; the host's duration="6s"); 0 stays until closed
 *   offline    (event) the connection went; also as the page starts without one (a page brought back
 *              from the history cache), and when Drive could not move to a page because of it
 *              (kitwork:offline): a click that did nothing is told why, and a closed notice opens again
 *   online     (event) it came back, after an offline was said; never as a page first loads, where
 *              nothing went missing, but a page that said offline and comes back from the history
 *              cache with the network back says it then
 *
 * The events are sent on the host (context.emit), the only element that hears them.
 *
 * The browser's word is half sure. offline is (no network at all); online only means a network is
 * there, not that the site answers through it (a café's Wi-Fi before its sign-in page). A request
 * that fails while the browser says online is the page's own error, not this component's.
 *
 * It reads navigator.onLine and the window's online and offline events itself rather than the
 * network service: that service also carries HTTP and uploads (about 4.7 kB), and a desktop or
 * mobile shell's web view fires the same two events.
 */
var NETWORK = Symbol("kit:network");

// reachable is the browser's word: false only when it is sure there is no network
function reachable() {
  try { return window.navigator.onLine !== false; } catch (problem) { return true; }
}

// time is a time the host gave (milliseconds), or the component's own when it gave none a number can be
function time(value, own) {
  var ms = Number(value);
  return isFinite(ms) && ms >= 0 ? ms : own;
}

// stop clears one of an instance's two clocks: wait (the delay before offline counts) or back (the
// duration "back online" stays)
function stop(own, clock) {
  if (own[clock]) {
    clearTimeout(own[clock]);
    own[clock] = 0;
  }
}

var networkDef = {
  connected: true,
  open: false,
  delay: 1000,
  duration: 4000,

  close: function () {
    var own = this[NETWORK];
    if (own) stop(own, "back");
    this.open = false;
  },

  init: function (context) {
    var scope = this;
    // said is the last event sent: "" before any, then "offline" or "online", so each change is told
    // once however many times the window repeats it, and online only ever follows an offline
    var own = this[NETWORK] = { said: "", wait: 0, back: 0 };

    function tell(event) {
      own.said = event;
      context.emit(event, { connected: event === "online" });
    }
    function offline() {
      stop(own, "wait");
      stop(own, "back");
      scope.connected = false;
      scope.open = true;
      tell("offline");
    }
    function check() {
      if (!reachable()) {
        // gone: counted once it has stayed gone for delay (at once for 0); already counted, or already
        // waiting, nothing more
        if (own.said === "offline" || own.wait) return;
        var delay = time(scope.delay, 1000);
        if (!delay) return offline();
        own.wait = setTimeout(function () {
          own.wait = 0;
          if (!reachable()) offline();
        }, delay);
        return;
      }
      // here: a break shorter than delay said nothing, so it has nothing to take back
      stop(own, "wait");
      if (own.said !== "offline") {
        scope.connected = true;
        return;
      }
      scope.connected = true;
      scope.open = true;
      tell("online");
      stop(own, "back");
      var duration = time(scope.duration, 4000);
      if (duration) {
        own.back = setTimeout(function () {
          own.back = 0;
          if (scope.connected) scope.open = false;
        }, duration);
      }
    }

    // The browser's own events, said at the window. This component's events bubble from its host up
    // to the window too, under the same two names; those are its own echo, not the browser's.
    function heard(event) { if (event.target === window) check(); }

    check();
    context.listen(window, "online", heard);
    context.listen(window, "offline", heard);
    // a page brought back from the history cache missed the events of the time it was away
    context.listen(window, "pageshow", function (event) { if (event.persisted) check(); });
    // Drive stayed on the page because the network is gone: say so again, the person just tried
    context.listen(document, "kitwork:offline", offline);
    context.cleanup(function () {
      stop(own, "wait");
      stop(own, "back");
    });
  }
};

window.kit.component("network", networkDef);
