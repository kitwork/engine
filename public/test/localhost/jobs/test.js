import { app, db, logger } from "../core.js"

// Tác vụ dọn dẹp hàng ngày
app.schedule(() => {
    db.users.delete()
    logger.info("Daily cleanup completed.")
}).daily("13:00")

// Tác vụ ping định kỳ
app.schedule(() => {
    logger.info("Ping từ Cron Job!")
}).every("5s")