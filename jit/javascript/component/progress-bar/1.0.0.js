// KitJS component: progress-bar@1.0.0
kit.component("progress-bar", {
    value: 0,
    status: "idle",

    get hidden() {
      return this.status === "idle";
    },

    get width() {
      return this.value + "%";
    },

    start() {
      this.value = 0;
      this.status = "running";
    },

    set(value) {
      this.value = Math.min(100, Math.max(0, Number(value) || 0));
      this.status = this.value >= 100 ? "completed" : "running";
      return this.value;
    },

    inc(amount) {
      amount = amount === null || amount === undefined ? 10 : Number(amount);
      return this.set(this.value + (Number.isFinite(amount) ? amount : 0));
    },

    done() {
      return this.set(100);
    },

    reset() {
      this.value = 0;
      this.status = "idle";
    }
});
