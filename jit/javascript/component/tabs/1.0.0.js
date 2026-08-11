// KitJS component: tabs@1.0.0
;(function (kit) {
  "use strict";

  function tabValue(tab) {
    return String(tab.getAttribute("data-tab") || "");
  }

  function tabs(host, tablist) {
    return Array.prototype.slice.call(tablist.querySelectorAll("[role='tab'][data-tab]")).filter(function (tab) {
      return tab.closest("[data-kit-component]") === host &&
        !tab.disabled && tab.getAttribute("aria-disabled") !== "true";
    });
  }

  function firstTablist(host) {
    return Array.prototype.slice.call(host.querySelectorAll("[role='tablist']")).filter(function (tablist) {
      return tablist.closest("[data-kit-component]") === host;
    })[0] || null;
  }

  kit.component("tabs", {
    activeTab: "overview",

    select(tab) {
      tab = String(tab || "");
      if (tab) this.activeTab = tab;
      return this.activeTab;
    },

    is(tab) {
      return this.activeTab === String(tab || "");
    },

    init() {
      var component = this;
      var host = this.$host;
      var tablist = firstTablist(host);
      if (tablist) {
        var items = tabs(host, tablist);
        if (items.length) {
          var requested = host.getAttribute("data-tabs-default") || "";
          var preferred = requested || component.activeTab;
          var selected = items.filter(function (tab) { return tabValue(tab) === preferred; })[0];
          if (!selected) {
            selected = items.filter(function (tab) { return tab.getAttribute("aria-selected") === "true"; })[0];
          }
          component.activeTab = tabValue(selected || items[0]);
        }
      }

      function onKeydown(event) {
        var tab = event.target.closest && event.target.closest("[role='tab'][data-tab]");
        if (!tab || !host.contains(tab) || tab.closest("[data-kit-component]") !== host) return;
        var currentTablist = tab.closest("[role='tablist']");
        if (!currentTablist || !host.contains(currentTablist)) return;

        var current = tabs(host, currentTablist);
        var index = current.indexOf(tab);
        var vertical = currentTablist.getAttribute("aria-orientation") === "vertical";
        var next = -1;
        if ((!vertical && event.key === "ArrowRight") || (vertical && event.key === "ArrowDown")) {
          next = (index + 1) % current.length;
        } else if ((!vertical && event.key === "ArrowLeft") || (vertical && event.key === "ArrowUp")) {
          next = (index - 1 + current.length) % current.length;
        } else if (event.key === "Home") next = 0;
        else if (event.key === "End") next = current.length - 1;
        if (next < 0 || !current[next]) return;

        event.preventDefault();
        component.select(tabValue(current[next]));
        current[next].focus();
      }

      host.addEventListener("keydown", onKeydown);
      return function () {
        host.removeEventListener("keydown", onKeydown);
      };
    }
  });
})(globalThis.kit);
