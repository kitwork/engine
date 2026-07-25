# Kitwork Cross-Platform Runtime API

## 1. Mục tiêu

Kitwork cung cấp một API thống nhất cho ứng dụng đa nền tảng:

```js
globalThis.kitwork
```

Ứng dụng chỉ gọi:

```js
await kitwork.camera.capture()
await kitwork.storage.get("theme")
await kitwork.files.read("notes/readme.md")
```

Runtime sẽ tự ánh xạ sang implementation phù hợp:

```text
Web        → Web APIs
Windows    → Native Windows APIs
macOS      → Cocoa APIs
Linux      → System APIs
Android    → Android SDK
iOS        → Apple Frameworks
```

Người phát triển không cần biết ứng dụng đang chạy trên nền tảng nào.

---

# 2. Cấu trúc tổng thể

```js
kitwork.runtime
kitwork.app
kitwork.platform
kitwork.permissions

kitwork.storage
kitwork.secureStorage
kitwork.cache
kitwork.database

kitwork.files
kitwork.dialog
kitwork.clipboard
kitwork.share

kitwork.camera
kitwork.media
kitwork.audio
kitwork.screen

kitwork.location
kitwork.device
kitwork.network
kitwork.battery
kitwork.sensor

kitwork.notification
kitwork.contacts
kitwork.calendar

kitwork.window
kitwork.display
kitwork.system
kitwork.shell

kitwork.http
kitwork.websocket
kitwork.sse

kitwork.auth
kitwork.session
kitwork.identity

kitwork.ai
kitwork.tasks
kitwork.events
kitwork.logs
```

---

# 3. Runtime

## 3.1 Thông tin runtime

```js
const runtime = await kitwork.runtime.info()
```

Kết quả:

```js
{
    name: "kitwork",
    version: "1.0.0",
    engine: "webview",
    bridgeVersion: "1",
    development: false
}
```

## 3.2 Kiểm tra capability

```js
const supported = await kitwork.runtime.supports("camera.capture")
```

```js
if (await kitwork.runtime.supports("location.current")) {
    const position = await kitwork.location.current()
}
```

## 3.3 Danh sách capability

```js
const capabilities = await kitwork.runtime.capabilities()
```

## 3.4 Runtime ready

```js
await kitwork.runtime.ready()
```

Hoặc:

```js
kitwork.runtime.onReady(() => {
    console.log("Kitwork runtime ready")
})
```

---

# 4. App

## 4.1 Thông tin ứng dụng

```js
const app = await kitwork.app.info()
```

```js
{
    id: "com.kitwork.example",
    name: "Example App",
    version: "1.2.0",
    build: "104"
}
```

## 4.2 Thoát ứng dụng

```js
await kitwork.app.exit()
```

## 4.3 Khởi động lại

```js
await kitwork.app.restart()
```

## 4.4 Trạng thái ứng dụng

```js
kitwork.app.on("foreground", () => {})
kitwork.app.on("background", () => {})
kitwork.app.on("pause", () => {})
kitwork.app.on("resume", () => {})
```

---

# 5. Platform

```js
const platform = await kitwork.platform.info()
```

```js
{
    os: "android",
    version: "16",
    architecture: "arm64",
    formFactor: "phone",
    language: "vi-VN",
    timezone: "Asia/Ho_Chi_Minh"
}
```

Kiểm tra nhanh:

```js
if (kitwork.platform.is("android")) {
    // Android-specific behavior
}
```

```js
kitwork.platform.is("web")
kitwork.platform.is("desktop")
kitwork.platform.is("mobile")
kitwork.platform.is("windows")
kitwork.platform.is("macos")
kitwork.platform.is("linux")
kitwork.platform.is("android")
kitwork.platform.is("ios")
```

---

# 6. Permissions

## 6.1 Kiểm tra quyền

```js
const status = await kitwork.permissions.check("camera")
```

Kết quả:

```js
"granted"
"denied"
"prompt"
"restricted"
"unsupported"
```

## 6.2 Yêu cầu quyền

```js
const status = await kitwork.permissions.request("camera")
```

## 6.3 Yêu cầu nhiều quyền

```js
const statuses = await kitwork.permissions.request([
    "camera",
    "microphone",
    "location"
])
```

## 6.4 Mở phần cài đặt

```js
await kitwork.permissions.openSettings()
```

---

# 7. Storage

Dùng cho dữ liệu key-value nhỏ và lâu dài.

```js
await kitwork.storage.set("theme", "dark")

const theme = await kitwork.storage.get("theme")

await kitwork.storage.remove("theme")

await kitwork.storage.clear()
```

## 7.1 Giá trị mặc định

```js
const theme = await kitwork.storage.get("theme", {
    default: "light"
})
```

## 7.2 Namespace

```js
const settings = kitwork.storage.namespace("settings")

await settings.set("theme", "dark")
await settings.set("language", "vi")
```

## 7.3 Liệt kê khóa

```js
const keys = await kitwork.storage.keys()
```

## 7.4 Kiểm tra tồn tại

```js
const exists = await kitwork.storage.has("theme")
```

---

# 8. Secure Storage

Dùng cho token, secret và credential.

```js
await kitwork.secureStorage.set("access_token", token)

const token = await kitwork.secureStorage.get("access_token")

await kitwork.secureStorage.remove("access_token")
```

Có thể yêu cầu xác thực sinh trắc học:

```js
const token = await kitwork.secureStorage.get("access_token", {
    authenticate: true,
    reason: "Xác nhận để đăng nhập"
})
```

Không nên lưu secret bằng:

```js
localStorage.setItem("token", token)
```

Nên dùng:

```js
await kitwork.secureStorage.set("token", token)
```

---

# 9. Cache

```js
await kitwork.cache.set("posts", posts, {
    ttl: 300
})
```

```js
const posts = await kitwork.cache.get("posts")
```

```js
await kitwork.cache.remove("posts")
await kitwork.cache.clear()
```

## 9.1 Cache theo namespace

```js
const apiCache = kitwork.cache.namespace("api")

await apiCache.set("latest-posts", posts, {
    ttl: 60
})
```

---

# 10. Database

## 10.1 Mở database

```js
const db = await kitwork.database.open("app")
```

## 10.2 Thực thi SQL

```js
await db.execute(`
    CREATE TABLE IF NOT EXISTS notes (
        id TEXT PRIMARY KEY,
        title TEXT,
        content TEXT,
        created_at TEXT
    )
`)
```

## 10.3 Query

```js
const notes = await db.query(
    "SELECT * FROM notes WHERE title LIKE ?",
    ["%kitwork%"]
)
```

## 10.4 Insert

```js
await db.execute(
    "INSERT INTO notes (id, title, content) VALUES (?, ?, ?)",
    ["note_1", "Kitwork", "Runtime notes"]
)
```

## 10.5 Transaction

```js
await db.transaction(async transaction => {
    await transaction.execute(
        "INSERT INTO users (id, name) VALUES (?, ?)",
        ["user_1", "Quốc"]
    )

    await transaction.execute(
        "INSERT INTO profiles (user_id) VALUES (?)",
        ["user_1"]
    )
})
```

## 10.6 Đóng database

```js
await db.close()
```

---

# 11. Files

## 11.1 Đọc file

```js
const content = await kitwork.files.read("documents/note.md")
```

## 11.2 Ghi file

```js
await kitwork.files.write(
    "documents/note.md",
    "# Hello Kitwork"
)
```

## 11.3 Ghi nhị phân

```js
await kitwork.files.write("images/photo.jpg", bytes, {
    encoding: "binary"
})
```

## 11.4 Kiểm tra tồn tại

```js
const exists = await kitwork.files.exists("documents/note.md")
```

## 11.5 Thông tin file

```js
const info = await kitwork.files.stat("documents/note.md")
```

```js
{
    path: "documents/note.md",
    name: "note.md",
    type: "file",
    size: 1204,
    createdAt: "...",
    modifiedAt: "..."
}
```

## 11.6 Liệt kê thư mục

```js
const files = await kitwork.files.list("documents")
```

## 11.7 Tạo thư mục

```js
await kitwork.files.mkdir("documents/projects", {
    recursive: true
})
```

## 11.8 Xóa

```js
await kitwork.files.remove("documents/note.md")
```

## 11.9 Sao chép và di chuyển

```js
await kitwork.files.copy("a.txt", "backup/a.txt")
await kitwork.files.move("a.txt", "archive/a.txt")
```

## 11.10 Thư mục hệ thống

```js
const paths = await kitwork.files.paths()
```

```js
{
    app: "...",
    data: "...",
    cache: "...",
    documents: "...",
    downloads: "...",
    temporary: "..."
}
```

---

# 12. Dialog

## 12.1 Chọn file

```js
const file = await kitwork.dialog.openFile()
```

```js
const files = await kitwork.dialog.openFile({
    multiple: true,
    types: ["image/png", "image/jpeg"]
})
```

## 12.2 Chọn thư mục

```js
const directory = await kitwork.dialog.openDirectory()
```

## 12.3 Lưu file

```js
const path = await kitwork.dialog.saveFile({
    suggestedName: "report.pdf"
})
```

## 12.4 Alert

```js
await kitwork.dialog.alert({
    title: "Thông báo",
    message: "Đã lưu thành công"
})
```

## 12.5 Confirm

```js
const confirmed = await kitwork.dialog.confirm({
    title: "Xóa dữ liệu",
    message: "Bạn có chắc chắn muốn xóa?"
})
```

## 12.6 Prompt

```js
const name = await kitwork.dialog.prompt({
    title: "Tên dự án",
    placeholder: "Nhập tên dự án"
})
```

---

# 13. Clipboard

```js
await kitwork.clipboard.writeText("Hello Kitwork")

const text = await kitwork.clipboard.readText()
```

```js
await kitwork.clipboard.writeImage(image)
const image = await kitwork.clipboard.readImage()
```

```js
await kitwork.clipboard.clear()
```

---

# 14. Share

```js
await kitwork.share.open({
    title: "Kitwork",
    text: "Một runtime đa nền tảng",
    url: "https://kitwork.io"
})
```

Chia sẻ file:

```js
await kitwork.share.files([
    "documents/report.pdf"
])
```

---

# 15. Camera

## 15.1 Chụp ảnh

```js
const photo = await kitwork.camera.capture()
```

```js
const photo = await kitwork.camera.capture({
    source: "camera",
    facing: "back",
    quality: 90,
    width: 1920,
    height: 1080,
    format: "jpeg"
})
```

Kết quả:

```js
{
    id: "media_01",
    path: "photos/photo_01.jpg",
    uri: "kitwork://media/photo_01",
    mime: "image/jpeg",
    width: 1920,
    height: 1080,
    size: 842120
}
```

## 15.2 Chọn ảnh

```js
const image = await kitwork.camera.pick()
```

```js
const images = await kitwork.camera.pick({
    multiple: true,
    limit: 10
})
```

## 15.3 Quay video

```js
const video = await kitwork.camera.record({
    facing: "back",
    quality: "high",
    maxDuration: 60
})
```

## 15.4 Scan QR

```js
const result = await kitwork.camera.scan({
    formats: ["qr"]
})
```

```js
{
    format: "qr",
    value: "https://kitwork.io"
}
```

## 15.5 Scan barcode

```js
const result = await kitwork.camera.scan({
    formats: ["ean13", "code128", "qr"]
})
```

## 15.6 OCR

```js
const result = await kitwork.camera.text({
    source: "camera",
    language: "vi"
})
```

```js
{
    text: "Nội dung nhận diện được",
    blocks: []
}
```

## 15.7 Xem trước camera

```js
const preview = await kitwork.camera.open({
    element: "#camera-preview",
    facing: "back"
})
```

```js
await preview.capture()
await preview.switchCamera()
await preview.close()
```

---

# 16. Media

## 16.1 Chọn media

```js
const media = await kitwork.media.pick({
    type: "image"
})
```

```js
const media = await kitwork.media.pick({
    type: "video",
    multiple: true
})
```

## 16.2 Metadata

```js
const info = await kitwork.media.info(media.path)
```

## 16.3 Resize ảnh

```js
const image = await kitwork.media.resize(photo.path, {
    width: 1024,
    quality: 80
})
```

## 16.4 Nén ảnh

```js
const image = await kitwork.media.compress(photo.path, {
    quality: 70
})
```

## 16.5 Thumbnail

```js
const thumbnail = await kitwork.media.thumbnail(video.path, {
    time: 2
})
```

---

# 17. Audio

## 17.1 Ghi âm

```js
const recording = await kitwork.audio.record({
    format: "m4a"
})
```

```js
await recording.pause()
await recording.resume()

const audio = await recording.stop()
```

## 17.2 Phát âm thanh

```js
const player = await kitwork.audio.play("audio/music.mp3")
```

```js
await player.pause()
await player.resume()
await player.seek(30)
await player.stop()
```

## 17.3 Trạng thái âm lượng

```js
await player.volume(0.8)
```

---

# 18. Screen

## 18.1 Chụp màn hình

```js
const image = await kitwork.screen.capture()
```

## 18.2 Quay màn hình

```js
const recording = await kitwork.screen.record({
    audio: true
})
```

```js
const video = await recording.stop()
```

## 18.3 Giữ màn hình sáng

```js
await kitwork.screen.keepAwake()
await kitwork.screen.allowSleep()
```

## 18.4 Độ sáng

```js
const brightness = await kitwork.screen.brightness()

await kitwork.screen.setBrightness(0.8)
```

---

# 19. Location

## 19.1 Vị trí hiện tại

```js
const position = await kitwork.location.current()
```

```js
{
    latitude: 15.5736,
    longitude: 108.4740,
    accuracy: 10,
    altitude: 5,
    speed: 0,
    timestamp: 1784920000000
}
```

## 19.2 Theo dõi vị trí

```js
const watcher = await kitwork.location.watch(position => {
    console.log(position)
})
```

```js
await watcher.stop()
```

## 19.3 Mở bản đồ

```js
await kitwork.location.openMap({
    latitude: 15.5736,
    longitude: 108.4740,
    label: "Tam Kỳ"
})
```

---

# 20. Device

```js
const device = await kitwork.device.info()
```

```js
{
    id: "device_generated_id",
    manufacturer: "Samsung",
    model: "Galaxy",
    os: "android",
    osVersion: "16",
    memory: 8589934592
}
```

## 20.1 Rung

```js
await kitwork.device.vibrate(200)
```

Hoặc pattern:

```js
await kitwork.device.vibrate([100, 50, 100])
```

## 20.2 Sinh trắc học

```js
const available = await kitwork.device.biometric.available()
```

```js
const result = await kitwork.device.biometric.authenticate({
    reason: "Xác nhận danh tính"
})
```

## 20.3 Giữ hướng màn hình

```js
await kitwork.device.orientation.lock("portrait")
await kitwork.device.orientation.unlock()
```

---

# 21. Network

```js
const status = await kitwork.network.status()
```

```js
{
    connected: true,
    type: "wifi",
    metered: false
}
```

Theo dõi thay đổi:

```js
kitwork.network.on("change", status => {
    console.log(status)
})
```

Kiểm tra kết nối:

```js
const online = await kitwork.network.online()
```

---

# 22. Battery

```js
const battery = await kitwork.battery.status()
```

```js
{
    level: 0.82,
    charging: true,
    lowPowerMode: false
}
```

```js
kitwork.battery.on("change", battery => {})
```

---

# 23. Sensors

## 23.1 Gia tốc kế

```js
const sensor = await kitwork.sensor.accelerometer.watch(data => {
    console.log(data.x, data.y, data.z)
})
```

```js
await sensor.stop()
```

## 23.2 Con quay hồi chuyển

```js
const sensor = await kitwork.sensor.gyroscope.watch(data => {})
```

## 23.3 La bàn

```js
const sensor = await kitwork.sensor.compass.watch(data => {
    console.log(data.heading)
})
```

---

# 24. Notifications

## 24.1 Notification cục bộ

```js
await kitwork.notification.send({
    title: "Kitwork",
    body: "Quá trình xử lý đã hoàn tất"
})
```

## 24.2 Notification theo thời gian

```js
await kitwork.notification.schedule({
    id: "reminder_1",
    title: "Nhắc nhở",
    body: "Kiểm tra dự án",
    at: "2026-07-26T09:00:00+07:00"
})
```

## 24.3 Hủy notification

```js
await kitwork.notification.cancel("reminder_1")
```

## 24.4 Push token

```js
const token = await kitwork.notification.pushToken()
```

## 24.5 Nhận sự kiện

```js
kitwork.notification.on("open", notification => {})
kitwork.notification.on("receive", notification => {})
```

---

# 25. Contacts

## 25.1 Chọn contact

```js
const contact = await kitwork.contacts.pick()
```

## 25.2 Liệt kê

```js
const contacts = await kitwork.contacts.list({
    search: "Nguyễn",
    limit: 50
})
```

## 25.3 Tạo contact

```js
await kitwork.contacts.create({
    name: "Nguyễn Văn A",
    phone: "+84901234567",
    email: "example@example.com"
})
```

---

# 26. Calendar

## 26.1 Tạo sự kiện

```js
await kitwork.calendar.create({
    title: "Kitwork Meeting",
    start: "2026-07-26T09:00:00+07:00",
    end: "2026-07-26T10:00:00+07:00",
    location: "Đà Nẵng"
})
```

## 26.2 Đọc sự kiện

```js
const events = await kitwork.calendar.events({
    from: "2026-07-01",
    to: "2026-07-31"
})
```

---

# 27. Window

Dành chủ yếu cho desktop.

## 27.1 Điều khiển cửa sổ

```js
await kitwork.window.minimize()
await kitwork.window.maximize()
await kitwork.window.restore()
await kitwork.window.close()
```

## 27.2 Fullscreen

```js
await kitwork.window.fullscreen(true)
await kitwork.window.fullscreen(false)
```

## 27.3 Tiêu đề

```js
await kitwork.window.setTitle("Kitwork Studio")
```

## 27.4 Kích thước

```js
await kitwork.window.resize({
    width: 1280,
    height: 800
})
```

## 27.5 Vị trí

```js
await kitwork.window.move({
    x: 100,
    y: 100
})
```

## 27.6 Luôn nằm trên cùng

```js
await kitwork.window.alwaysOnTop(true)
```

## 27.7 Tạo cửa sổ mới

```js
const windowRef = await kitwork.window.open({
    url: "/settings",
    width: 800,
    height: 600
})
```

---

# 28. Display

```js
const displays = await kitwork.display.list()
```

```js
const primary = await kitwork.display.primary()
```

```js
{
    id: "display_1",
    width: 3840,
    height: 2160,
    scale: 1.5,
    primary: true
}
```

---

# 29. System

## 29.1 Chủ đề hệ thống

```js
const theme = await kitwork.system.theme()
```

```js
kitwork.system.on("themeChange", theme => {})
```

## 29.2 Ngôn ngữ

```js
const locale = await kitwork.system.locale()
```

## 29.3 Mở phần cài đặt

```js
await kitwork.system.openSettings()
```

## 29.4 Sleep và shutdown

Chỉ hỗ trợ khi ứng dụng có quyền phù hợp:

```js
await kitwork.system.sleep()
await kitwork.system.shutdown()
await kitwork.system.restart()
```

---

# 30. Shell

## 30.1 Mở URL bên ngoài

```js
await kitwork.shell.open("https://kitwork.io")
```

## 30.2 Mở file bằng ứng dụng mặc định

```js
await kitwork.shell.openFile("documents/report.pdf")
```

## 30.3 Hiển thị file trong thư mục

```js
await kitwork.shell.reveal("documents/report.pdf")
```

## 30.4 Chạy command

Chỉ nên hỗ trợ cho desktop hoặc môi trường developer:

```js
const result = await kitwork.shell.exec("git", [
    "status"
])
```

```js
{
    code: 0,
    stdout: "...",
    stderr: ""
}
```

API này phải bị giới hạn quyền nghiêm ngặt.

---

# 31. HTTP

```js
const response = await kitwork.http.get(
    "https://api.example.com/posts"
)
```

```js
const response = await kitwork.http.post(
    "https://api.example.com/posts",
    {
        title: "Kitwork"
    }
)
```

## 31.1 Request đầy đủ

```js
const response = await kitwork.http.request({
    method: "POST",
    url: "https://api.example.com/posts",
    headers: {
        Authorization: `Bearer ${token}`
    },
    body: {
        title: "Kitwork"
    },
    timeout: 10000
})
```

Kết quả:

```js
{
    status: 200,
    headers: {},
    data: {},
    url: "https://api.example.com/posts"
}
```

## 31.2 Upload file

```js
const result = await kitwork.http.upload({
    url: "https://api.example.com/upload",
    file: photo.path,
    field: "image"
})
```

## 31.3 Download file

```js
const download = await kitwork.http.download({
    url: "https://example.com/report.pdf",
    destination: "downloads/report.pdf"
})
```

```js
download.on("progress", progress => {
    console.log(progress.percent)
})
```

---

# 32. WebSocket

```js
const socket = await kitwork.websocket.connect(
    "wss://example.com/socket"
)
```

```js
socket.on("open", () => {})
socket.on("message", message => {})
socket.on("close", () => {})
socket.on("error", error => {})
```

```js
await socket.send({
    type: "message",
    content: "Hello"
})
```

```js
await socket.close()
```

---

# 33. Server-Sent Events

```js
const stream = await kitwork.sse.connect(
    "https://example.com/events"
)
```

```js
stream.on("message", event => {})
stream.on("error", error => {})
```

```js
await stream.close()
```

---

# 34. Session

Dùng để quản lý phiên ứng dụng.

```js
await kitwork.session.set("user", user)
const user = await kitwork.session.get("user")
```

## 34.1 Xóa phiên

```js
await kitwork.session.clear()
```

## 34.2 Phiên đăng nhập

```js
await kitwork.session.start({
    accessToken,
    refreshToken,
    expiresAt
})
```

```js
const session = await kitwork.session.current()
```

```js
await kitwork.session.end()
```

---

# 35. Auth

## 35.1 Đăng nhập

```js
const session = await kitwork.auth.login({
    email,
    password
})
```

## 35.2 OAuth

```js
const session = await kitwork.auth.oauth({
    provider: "google"
})
```

## 35.3 Đăng xuất

```js
await kitwork.auth.logout()
```

## 35.4 Sinh trắc học

```js
await kitwork.auth.biometric()
```

## 35.5 Theo dõi thay đổi

```js
kitwork.auth.on("change", session => {})
```

---

# 36. Identity

```js
const identity = await kitwork.identity.current()
```

```js
{
    userId: "user_01",
    deviceId: "device_01",
    tenantId: "tenant_01",
    sessionId: "session_01"
}
```

Chuyển tenant:

```js
await kitwork.identity.switchTenant("tenant_02")
```

---

# 37. AI

## 37.1 Chat

```js
const response = await kitwork.ai.chat({
    model: "default",
    messages: [
        {
            role: "user",
            content: "Viết tóm tắt nội dung này"
        }
    ]
})
```

## 37.2 Streaming

```js
const stream = await kitwork.ai.chat({
    messages,
    stream: true
})
```

```js
for await (const chunk of stream) {
    console.log(chunk.text)
}
```

## 37.3 Embedding

```js
const embedding = await kitwork.ai.embed({
    input: "Kitwork Runtime"
})
```

## 37.4 OCR

```js
const result = await kitwork.ai.vision.text(photo.path)
```

## 37.5 Phân tích hình ảnh

```js
const result = await kitwork.ai.vision.describe(photo.path)
```

## 37.6 Speech-to-text

```js
const text = await kitwork.ai.transcribe(audio.path)
```

## 37.7 Text-to-speech

```js
const audio = await kitwork.ai.speak({
    text: "Xin chào từ Kitwork",
    language: "vi"
})
```

---

# 38. Background Tasks

## 38.1 Đăng ký task

```js
await kitwork.tasks.register("sync-content", async context => {
    await syncContent()
})
```

## 38.2 Chạy task

```js
await kitwork.tasks.run("sync-content")
```

## 38.3 Schedule task

```js
await kitwork.tasks.schedule("sync-content", {
    every: "15m"
})
```

## 38.4 Hủy task

```js
await kitwork.tasks.cancel("sync-content")
```

## 38.5 Trạng thái task

```js
const status = await kitwork.tasks.status("sync-content")
```

---

# 39. Events

Event bus nội bộ:

```js
kitwork.events.on("user.updated", user => {
    console.log(user)
})
```

```js
await kitwork.events.emit("user.updated", {
    id: "user_01"
})
```

Hủy listener:

```js
const unsubscribe = kitwork.events.on("user.updated", handler)

unsubscribe()
```

---

# 40. Logs

```js
kitwork.logs.debug("Debug message")
kitwork.logs.info("Application started")
kitwork.logs.warn("Connection is slow")
kitwork.logs.error("Failed to load data", error)
```

## 40.1 Scope

```js
const logger = kitwork.logs.scope("database")

logger.info("Database opened")
logger.error("Query failed", error)
```

## 40.2 Đọc log

```js
const logs = await kitwork.logs.list({
    level: "error",
    limit: 100
})
```

## 40.3 Xuất log

```js
const file = await kitwork.logs.export()
```

---

# 41. Error chuẩn

Tất cả API nên trả về lỗi cùng một định dạng:

```js
{
    name: "KitworkError",
    code: "PERMISSION_DENIED",
    message: "Camera permission was denied",
    module: "camera",
    action: "capture",
    details: {}
}
```

Ví dụ:

```js
try {
    const photo = await kitwork.camera.capture()
} catch (error) {
    if (error.code === "PERMISSION_DENIED") {
        await kitwork.permissions.openSettings()
    }
}
```

Một số mã lỗi chuẩn:

```text
UNSUPPORTED
UNAVAILABLE
PERMISSION_DENIED
PERMISSION_RESTRICTED
INVALID_ARGUMENT
NOT_FOUND
ALREADY_EXISTS
CANCELLED
TIMEOUT
NETWORK_ERROR
AUTHENTICATION_REQUIRED
ACCESS_DENIED
STORAGE_FULL
INTERNAL_ERROR
```

---

# 42. Response chuẩn

Các operation đơn giản:

```js
{
    ok: true,
    value: ...
}
```

Hoặc dùng Promise trực tiếp:

```js
const value = await kitwork.storage.get("theme")
```

Khuyến nghị ưu tiên Promise trực tiếp. Khi lỗi, API throw `KitworkError`.

---

# 43. Bridge Protocol

## 43.1 Request từ JavaScript

```json
{
    "id": "req_01",
    "module": "camera",
    "action": "capture",
    "params": {
        "quality": 90
    }
}
```

## 43.2 Response thành công

```json
{
    "id": "req_01",
    "ok": true,
    "result": {
        "path": "photos/photo_01.jpg"
    }
}
```

## 43.3 Response lỗi

```json
{
    "id": "req_01",
    "ok": false,
    "error": {
        "code": "PERMISSION_DENIED",
        "message": "Camera permission was denied"
    }
}
```

## 43.4 Event từ native

```json
{
    "type": "event",
    "module": "network",
    "event": "change",
    "data": {
        "connected": false
    }
}
```

---

# 44. JavaScript Bridge

```js
class KitworkBridge {
    constructor(transport) {
        this.transport = transport
        this.pending = new Map()
        this.sequence = 0
    }

    invoke(module, action, params = {}) {
        return new Promise((resolve, reject) => {
            const id = `req_${++this.sequence}`

            this.pending.set(id, {
                resolve,
                reject
            })

            this.transport.send({
                id,
                module,
                action,
                params
            })
        })
    }

    resolve(message) {
        const pending = this.pending.get(message.id)

        if (!pending) {
            return
        }

        this.pending.delete(message.id)

        if (message.ok) {
            pending.resolve(message.result)
            return
        }

        const error = new Error(message.error.message)
        error.name = "KitworkError"
        error.code = message.error.code
        error.details = message.error.details

        pending.reject(error)
    }
}
```

Module camera:

```js
class CameraModule {
    constructor(bridge) {
        this.bridge = bridge
    }

    capture(options = {}) {
        return this.bridge.invoke(
            "camera",
            "capture",
            options
        )
    }

    pick(options = {}) {
        return this.bridge.invoke(
            "camera",
            "pick",
            options
        )
    }

    record(options = {}) {
        return this.bridge.invoke(
            "camera",
            "record",
            options
        )
    }

    scan(options = {}) {
        return this.bridge.invoke(
            "camera",
            "scan",
            options
        )
    }
}
```

Khởi tạo global:

```js
globalThis.kitwork = {
    camera: new CameraModule(bridge),
    storage: new StorageModule(bridge),
    files: new FilesModule(bridge),
    device: new DeviceModule(bridge)
}
```

---

# 45. Quyền trong manifest

Ứng dụng phải khai báo capability muốn sử dụng:

```js
export default {
    app: {
        id: "com.example.app",
        name: "Example App"
    },

    permissions: [
        "camera",
        "microphone",
        "location",
        "notifications"
    ],

    capabilities: {
        files: {
            read: [
                "documents/**"
            ],
            write: [
                "documents/**",
                "downloads/**"
            ]
        },

        network: {
            domains: [
                "api.example.com",
                "*.kitwork.io"
            ]
        },

        shell: false
    }
}
```

Runtime chỉ expose các API đã được cho phép.

---

# 46. Phân loại API

## Web-compatible

Những API có thể dùng Web API phía dưới:

```text
storage
clipboard
camera
location
notifications
network
share
```

## Native bridge required

Những API thường cần native bridge:

```text
secureStorage
database
files
window
system
contacts
calendar
background tasks
biometric
shell
native HTTP
```

## Optional capabilities

Một nền tảng có thể không hỗ trợ toàn bộ API:

```js
if (!await kitwork.runtime.supports("window.minimize")) {
    // Không hiển thị nút minimize
}
```

---

# 47. Nguyên tắc thiết kế

## Một global duy nhất

```js
globalThis.kitwork
```

Không tạo hàng loạt global như:

```js
camera
storage
device
filesystem
native
```

## API theo mục đích

Tốt:

```js
await kitwork.camera.capture()
await kitwork.secureStorage.set()
await kitwork.share.open()
```

Không nên:

```js
await kitwork.callNativeMethod("captureCamera")
```

## Promise-first

```js
const photo = await kitwork.camera.capture()
```

Không dùng callback:

```js
kitwork.camera.capture(photo => {}, error => {})
```

## Capability-aware

```js
await kitwork.runtime.supports("camera.capture")
```

## Permission-aware

```js
await kitwork.permissions.request("camera")
```

## Secure by default

Không tự động cho phép:

```text
shell execution
arbitrary file access
arbitrary network access
system shutdown
credential access
```

## Platform-independent

Code ứng dụng không nên chứa:

```js
if (android) {
    callCameraX()
}

if (ios) {
    callAVFoundation()
}
```

Chỉ cần:

```js
await kitwork.camera.capture()
```

---

# 48. Ví dụ ứng dụng hoàn chỉnh

```js
await kitwork.runtime.ready()

const permission = await kitwork.permissions.request("camera")

if (permission !== "granted") {
    await kitwork.dialog.alert({
        title: "Không có quyền camera",
        message: "Hãy cấp quyền camera trong cài đặt."
    })

    throw new Error("Camera permission denied")
}

const photo = await kitwork.camera.capture({
    quality: 85,
    facing: "back"
})

const optimized = await kitwork.media.resize(photo.path, {
    width: 1280,
    quality: 80
})

const upload = await kitwork.http.upload({
    url: "https://api.example.com/photos",
    file: optimized.path,
    field: "photo"
})

await kitwork.storage.set("last_uploaded_photo", {
    path: optimized.path,
    uploadedAt: new Date().toISOString(),
    remoteUrl: upload.url
})

await kitwork.notification.send({
    title: "Tải ảnh hoàn tất",
    body: "Ảnh của bạn đã được tải lên."
})
```

---

# 49. Bộ API cốt lõi cho phiên bản đầu tiên

Kitwork không nên triển khai toàn bộ ngay từ đầu.

Phiên bản đầu tiên chỉ cần:

```text
kitwork.runtime
kitwork.platform
kitwork.permissions

kitwork.storage
kitwork.secureStorage
kitwork.database
kitwork.files

kitwork.dialog
kitwork.clipboard
kitwork.share

kitwork.camera
kitwork.location
kitwork.notification

kitwork.device
kitwork.network

kitwork.window
kitwork.shell

kitwork.http
kitwork.events
kitwork.logs
```

Đây đã đủ để phát triển phần lớn:

```text
desktop applications
mobile applications
local-first applications
content applications
AI applications
business tools
multi-tenant clients
```

Các module như contacts, calendar, sensors, screen recording và AI có thể bổ sung sau.

---

# 50. Định vị

Kitwork không chỉ là một WebView wrapper.

Nó là một runtime cung cấp:

```text
One API.
One runtime.
Every platform.
```

Hoặc:

```text
One namespace.
Every native capability.
```

Ứng dụng viết một lần:

```js
await kitwork.camera.capture()
```

Kitwork quyết định cách thực thi trên từng nền tảng.
