# Config dạng `server.run()` — bootstrap chạy được

Engine nạp config từ một file `.kitwork.js` (ngoài `.json`/`.yaml`). Thay vì dữ
liệu tĩnh, bạn viết một **chương trình bootstrap** kết thúc bằng `server.run({...})` —
giống `Bun.serve()` / `Deno.serve()`: JS biểu đạt ý định, Go thật sự phục vụ.

```js
import { server, env } from "kitwork";
server.run({ port: env.int("PORT", 3000), root: "./tenants" });
```

## Hoạt động thế nào
1. `engine.Run("server.kitwork.js")` thấy đuôi `.js` → gọi `evalConfigJS`.
2. Chạy file trong **VM setup tối giản**: builtin chỉ có `server` + `env`
   (KHÔNG router/database/runtime của tenant — và `env` **không** lộ cho tenant/capsule
   vì chứa secret).
3. `server.run(obj)` **bắt** object config (builtin bàn giao, không tự phục vụ).
4. Object → `map[string]interface{}` qua `json.Marshal` của `value.Value` → đẩy vào
   `ParseConfig` y như nguồn `.json`/`.yaml`.
5. Go nhận config → `ListenAndServe`.

An toàn vì subset Kitwork **cấm `while` + có gas** → config-bằng-code không thể treo.

## `env`
Chỉ một cách: **`env.KEY`** — tự ép kiểu `"8080"`→số, `"true"/"false"`→bool, còn lại→chuỗi.

| Cách dùng | Ý nghĩa |
|---|---|
| `env.PORT` | giá trị đã ép kiểu (số/bool/chuỗi), `nil` nếu thiếu |
| `env.PORT \|\| 8080` | có default (kiểu JS) |
| `env.require("KEY")` | bắt buộc — thiếu là **BOOT FAIL**, log tên biến |

Lưu ý duy nhất: **default-true** không viết `env.X || true` được (`"false" || true` = true) —
muốn bật mặc định thì hardcode `true`; còn lại `env.X || false` an toàn.

`env` là **Proxy** nên `env.KEY` không đụng method ép-kiểu built-in của value.

## Vì sao `server.run({data})` (chứ không phải `export default {}` hay setter rời rạc)
- **Cảm giác runtime thật** (đúng chất Kitwork) — file là chương trình, chạy được.
- Đối số vẫn là **dữ liệu khai báo** → validate được, không trạng thái nửa vời, và
  có thể "resolve sẵn ra JSON" cho prod nếu muốn (chạy dry → dump `server.run` arg).
- Chỗ tự nhiên cho **hành vi** sau này (cron/middleware) mà JSON không chứa được.

Xem `server.kitwork.js` cạnh file này để có ví dụ đầy đủ.
