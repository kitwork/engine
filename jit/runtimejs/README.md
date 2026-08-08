# Kitwork RuntimeJS (Sibling Package of Hydrate)

Mục này chứa bộ Client Runtime JS mới cho Kitwork, ngang cấp với `jit/hydrate`:

- **Đồng cấp với `jit/hydrate`**: Nằm trực tiếp dưới `engine/jit/runtimejs`.
- **Pure Plain Object Architecture**: Khai báo trực tiếp các Plain Objects trên `kit.*`.
- **Zero Boilerplate**: 100% Native JS, tự động nhận diện Public Methods và Getters/Setters.
- **Support `data-kit-keep`**: Bảo vệ các Overlay Nodes khỏi DOM Morphing khi chuyển trang SPA Drive.

## Cấu trúc các file Dịch vụ

- `theme.js`: Quản lý Light/Dark mode (`kit.theme`)
- `clipboard.js`: Đọc/Ghi Clipboard (`kit.clipboard`)
- `announce.js`: Hỗ trợ người khiếm thị Screen Reader A11y (`kit.announce`)
- `progress.js`: Điều khiển Top Progress Bar (`kit.progress`)
