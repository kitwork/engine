kit.component("theme", {
    mode: kit.theme.resolved, // Lấy trạng thái ban đầu
    toggle: function () {
        kit.theme.toggle();             // Gọi dịch vụ nền tảng
        this.mode = kit.theme.resolved; // Cập nhật lại state của Component
    }
});