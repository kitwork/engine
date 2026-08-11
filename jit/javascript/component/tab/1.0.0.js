// KitJS compatibility component: tab@1.0.0
// New interfaces should use component:tabs.
kit.component("tab", {
    active: "tab1",

    select(name) {
      this.active = String(name || "");
      return this.active;
    },

    is(name) {
      return this.active === String(name || "");
    }
});
