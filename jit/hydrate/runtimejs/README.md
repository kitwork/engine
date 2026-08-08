# Kitwork RuntimeJS (Pure Plain-Object Client Services & Components)

Mục này chứa bộ Client Runtime JS mới cho Kitwork theo chuẩn **Plain Object Architecture**:

- **Không dùng String Switch Dispatchers**: Khai báo trực tiếp các Plain Objects trên `kit.*`.
- **Zero Boilerplate**: 100% Native JS, tự động nhận diện Public Methods và Getters/Setters.
- **Support `data-kit-keep`**: Bảo vệ các Overlay Nodes khỏi DOM Morphing khi chuyển trang SPA Drive.

## Cấu trúc dự kiến

- `theme.js`: Quản lý Light/Dark mode (`kit.theme`)
- `clipboard.js`: Đọc/Ghi Clipboard (`kit.clipboard`)
- `announce.js`: Hỗ trợ người khiếm thị Screen Reader A11y (`kit.announce`)
- `progress.js`: Điều khiển Top Progress Bar (`kit.progress`)
- `web.js`: Web Dialogs & Web APIs (`kit.dialog`, `kit.share`)
