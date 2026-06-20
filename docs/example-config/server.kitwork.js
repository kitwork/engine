// server.kitwork.js — bootstrap CHẠY ĐƯỢC cho engine (cấp host).
//
// Engine chạy file này 1 lần lúc boot, bắt object từ server.run({...}), rồi Go
// ListenAndServe (Bun/Deno-style). Không cần YAML/parser ngoài.
//
// env.KEY tự ép kiểu: "8080"→số, "true"/"false"→bool, còn lại→chuỗi.
//   default kiểu JS:  env.PORT || 8080
//   bắt buộc:         env.require("KEY")   (thiếu → BOOT FAIL, log rõ tên)

import { server, env } from "kitwork";

server.run({
  port: env.PORT || 3000,
  host: env.HOST || "0.0.0.0",
  root: env.ROOT || "tenants",
  allow_local: env.ALLOW_LOCAL || false,

  rate_limit: {
    // Lưu ý: default-true KHÔNG dùng `|| true` được ("false" || true = true).
    // Cần bật mặc định thì hardcode true; còn lại dùng `|| false`.
    enabled: env.RATE_LIMIT || false,
    rate: env.RATE || 2000
  },

  databases: [
    {
      alias: "main",
      type: "sqlite",
      name: env.DB_NAME || "data.db"
    },
    {
      alias: "system",
      type: env.SYS_DB_TYPE || "postgres",
      host: env.SYS_DB_HOST || "127.0.0.1",
      port: env.SYS_DB_PORT || 5432,
      user: env.SYS_DB_USER || "kitwork",
      password: env.require("SYS_DB_PASSWORD")
    }
  ]
});
