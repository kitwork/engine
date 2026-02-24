import work from "kitwork"

// Khởi tạo và thiết lập tài nguyên
const app = work({ debug: true })
const entity = app.entity()
const logger = app.log({ folder: "/logs" })

const http = app.http({ timeout: "10s", headers: { "User-Agent": "kitwork" } })
const smtp = app.smtp({
    host: "localhost",
    port: 25,
    user: "[EMAIL_ADDRESS]",
    password: "[PASSWORD]"
})

const chrome = app.chrome({
    headless: true,
    sandbox: false,
    timeout: "10s"
})

const db = app.database({
    host: "localhost",
    user: "root",
    password: "123456",
    database: "test"
})

// Mở rộng DB theo Tenant (Nâng cao)
const dbPgl = app.postgres(entity.database)
const dbRedis = app.redis(entity.redis)

// Export Named Module (gom lại 1 dòng cuối) 
export { app, entity, logger, http, smtp, chrome, db, dbPgl, dbRedis }