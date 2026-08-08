// Dịch vụ Quản lý Theme (Light / Dark Mode)
// Cú pháp: Plain Object với Getters & Setters
kit.theme = {
  get mode() {
    return localStorage.getItem("theme") || "system";
  },

  get resolved() {
    return this.mode === "system"
      ? (matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light")
      : this.mode;
  },

  set: function (mode) {
    localStorage.setItem("theme", mode);
    document.documentElement.classList.toggle("dark", this.resolved === "dark");
  },

  toggle: function () {
    this.set(this.resolved === "dark" ? "light" : "dark");
  }
};
