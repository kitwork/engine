// ============================================================================
// Kitwork Client Runtime Service: Progress (1.0.0)
// ============================================================================
// Location: engine/jit/javascript/service/progress/1.0.0.js
// ============================================================================
// Service chỉ quản lý Trạng thái & Logic thuần túy (Pure State & Bus Logic).
// Không inject DOM/CSS trực tiếp -> Để phần giao diện cho Component đảm nhận!
// ============================================================================

(function (window) {
  "use strict";

  var kit = window.kit = window.kit || {};

  if (kit.progress) return;

  var timer = null;

  kit.progress = {
    // Trạng thái phần trăm hiện tại (0 -> 100)
    value: 0,

    // Trạng thái tiến trình: "idle" | "running" | "completed"
    status: "idle",

    // 1. Bắt đầu chạy tiến trình (Tự động tăng từ 0% -> 80%)
    start: function () {
      if (timer) clearInterval(timer);
      this.value = 0;
      this.status = "running";

      var self = this;
      timer = setInterval(function () {
        if (self.value < 80) {
          self.value = Math.min(80, self.value + (80 - self.value) * 0.1);
        }
      }, 200);

      return Promise.resolve(this.value);
    },

    // 2. Thiết lập phần trăm tiến trình cụ thể (0 - 100)
    set: function (val) {
      if (timer) clearInterval(timer);
      this.value = Math.min(100, Math.max(0, Number(val) || 0));
      this.status = this.value >= 100 ? "completed" : "running";
      return Promise.resolve(this.value);
    },

    // 3. Tăng phần trăm tiến trình
    inc: function (amount) {
      return this.set(this.value + (Number(amount) || 10));
    },

    // 4. Hoàn tất tiến trình (100%)
    done: function () {
      if (timer) clearInterval(timer);
      this.value = 100;
      this.status = "completed";

      var self = this;
      setTimeout(function () {
        if (self.status === "completed") {
          self.status = "idle";
          self.value = 0;
        }
      }, 300);

      return Promise.resolve(100);
    }
  };

})(typeof window !== "undefined" ? window : globalThis);
