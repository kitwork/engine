// Dịch vụ Đọc/Ghi Clipboard
kit.clipboard = {
  writeText: function (text) {
    return navigator.clipboard ? navigator.clipboard.writeText(text) : Promise.reject("Clipboard API not supported");
  },

  readText: function () {
    return navigator.clipboard ? navigator.clipboard.readText() : Promise.resolve("");
  }
};
