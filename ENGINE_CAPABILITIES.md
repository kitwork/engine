# 🚀 Kitwork Engine: Advanced Capabilities Guide

Tài liệu này hướng dẫn các tính năng cao cấp của Kitwork Engine - Hệ thống thực thi workflow ưu tiên hiệu năng và tận dụng tối đa sức mạnh của Hệ điều hành.

## 1. Kiểm soát luồng thực thi (Execution Control)

### Done & Fail Hooks (Lifecycle)
Quản lý trạng thái kết thúc của task một cách tường minh, tách biệt logic nghiệp vụ và logic hậu mãi.

*   **`done(callback)`**: Chạy khi task hoàn tất thành công.
*   **`fail(callback)`**: Chạy khi task gặp lỗi hoặc gọi hàm `fail()`.

```javascript
work("user.create")
    .handle((req) => {
        if (!req.body().name) fail("Missing Name");
        return db.user.insert(req.body());
    })
    .done((res) => log(`Thành công: ${res.id}`))
    .fail((err) => log(`Lỗi hệ thống: ${err}`));
```

---

## 2. Hệ thống Cache & Stacking

### Cache (RAM-based)
Lưu kết quả trong bộ nhớ đệm LRU. Thích hợp cho dữ liệu nhỏ, cần tốc độ cực cao.
*   **Cú pháp**: `.cache("5s")` hoặc `.cache(60)` (giây).

### Static (Disk-based Snapshot)
"Tĩnh hóa" kết quả của Script ra đĩa cứng dưới dạng file tĩnh đơn lẻ (Single File with Offset). Tận dụng **Metadata (ModTime/ExpireAt)** của cache để kiểm soát hạn dùng.
*   **Cú pháp**: `.static("1h")` hoặc `.static({ duration: "1h" })`.
*   **Kiến trúc tối ưu**: Lưu trữ metadata (status code, content-type, headers) và response body trong cùng một file duy nhất dạng nhị phân có offset, giúp giảm 50% số lượng tệp tin trên ổ đĩa, loại bỏ lãng phí bộ nhớ vật lý đĩa cứng (slack space block) và stream dữ liệu trực tiếp với RAM overhead bằng 0.


---

## 3. Tài nguyên Tĩnh (Unified Assets Serving)

Tự động nhận diện và phục vụ tài nguyên từ đĩa cứng với tốc độ **Zero-VM** (không chạy Script).

### Assets (Smart Resource Mapping)
Hàm `.assets()` là hàm đa năng, tự động nhận diện đường dẫn là File hay Thư mục.

*   **Single File**:
    ```javascript
    work("logo").router("GET", "/logo.png").assets("./public/img/logo.png");
    ```
*   **Directory (Kho tài nguyên)**:
    ```javascript
    work("static").router("GET", "/static/*").assets("./dist/static");
    ```

*Lưu ý: Bạn cũng có thể dùng `.file()` như một bí danh (alias) của `.assets()` nếu muốn code rõ nghĩa hơn khi trỏ tới 1 file duy nhất.*

---

## 4. Xử lý dữ liệu Functional

Kitwork hỗ trợ các hàm biến đổi dữ liệu bậc cao ngay trong Core, chạy với hiệu năng Bytecode tối ưu.

*   **`.map(item => ({...}))`**: Biến đổi danh sách.
*   **`.filter(item => condition)`**: Lọc danh sách.
*   **`.find(item => condition)`**: Tìm kiếm phần tử.

**QUAN TRỌNG**: Khi trả về Object trong Arrow Function, bắt buộc dùng ngoặc đơn: `item => ({ id: item.id })`.

---

---
## 5. Triết lý Thiết kế (Developer Mindset)

1.  **Fast-Path First**: Nếu có thể dùng `.assets()` hoặc `.static()`, hãy dùng chúng để bypass bộ máy Script.
2.  **Explicit Exit**: Dùng `done()` và `fail()` thay vì lồng `if-else`.
3.  **OS-Native Integrity**: Tận dụng File Metadata của OS là cách tốt nhất để quản lý Cache bền bỉ và hiệu quả.

---

## 6. Định hướng phát triển tương lai (Roadmap)

### 1. Egress Network Policies (Bảo mật Sandbox)
Bổ sung cấu hình danh sách domain được phép gọi (`egress_allowed_domains`) trong cấu hình YAML của mỗi Tenant. Tự động kiểm tra và chặn các yêu cầu HTTP từ VM gọi tới mạng nội bộ (`localhost`, `192.168.*`) để phòng chống tấn công SSRF.

### 2. Durable Worker Queue & Background Jobs (Hàng đợi tác vụ nền bền bỉ)
Thay vì thực thi Goroutine trực tiếp trên RAM qua hàm `go()`, phát triển hệ thống Queue có sự bền bỉ (Durable). Các task nền sẽ được lưu vào cơ sở dữ liệu và xử lý bởi Go-native Worker Pools hỗ trợ retry (exponential backoff) và hàng đợi lỗi (DLQ).

### 3. Distributed Shared State (Bộ nhớ đệm phân tán)
Cung cấp Driver kết nối Redis hoặc cơ chế đồng bộ Cluster State qua gRPC để chia sẻ dữ liệu bộ nhớ đệm `cache` của các Tenant khi chạy mở rộng trên nhiều máy chủ vật lý.

## 7. Mô-đun hóa & Bundling (Multi-File ESM)

Kitwork Engine tự động hỗ trợ tính năng chia nhỏ mã nguồn thành nhiều file JavaScript bằng cú pháp standard ES Module (`import` và `export`). Bộ biên dịch sẽ tự động bundle code bằng Esbuild ở thời điểm compile-time mà không cần cài đặt Node.js hay bất kỳ build-tool nào khác.

### Cú pháp cơ bản
*   **Khai báo và xuất module**:
    ```javascript
    // helper.js
    export const getHello = () => {
        return "Hello from helper module!";
    };
    ```
*   **Import và sử dụng**:
    ```javascript
    // app.kitwork.js
    import { router } from "kitwork"; // Module kitwork ảo được cung cấp sẵn
    import { getHello } from "./helper.js";

    router.get("/test").handle((response) => response.text(getHello()));
    ```

### ⚠️ Lưu ý Cực kỳ Quan trọng
Do Bộ phân tích cú pháp (Parser) của Kitwork JS Engine được tối ưu hóa siêu nhỏ gọn và **không hỗ trợ từ khóa `function`**, bạn **bắt buộc** phải tuân thủ quy tắc sau:
1.  Chỉ định nghĩa hàm bằng **Arrow Function** (`const myFunc = () => {}`).
2.  Không sử dụng từ khóa `function` truyền thống (như `function myFunc() {}` hay `export function myFunc() {}`), nếu không bộ biên dịch sẽ báo lỗi cú pháp (`assemble error`).

---

## 8. Lớp tương thích JavaScript (JS Compatibility Layer)

Engine cung cấp các global chuẩn JavaScript để lập trình viên JS làm việc tự nhiên, không phải học API riêng:

### Toán tử & cú pháp
*   **`%` (modulo)**: `17 % 5` → `2` — đầy đủ qua pipeline lexer → compiler → opcode `MOD`.
*   **`new`**: được chấp nhận như tiền tố constructor chuẩn JS (`new Date()`). Kitwork không dùng prototype-based class — builtin tự trả về object — nên `new` là tương thích cú pháp.
*   **`===` / `!==`**: strict equality chuẩn JS. So sánh của Kitwork vốn đã strict theo Kind nên `===` ≡ `==`.
*   **Ternary `cond ? a : b`**: hỗ trợ đầy đủ, kể cả lồng nhau kết hợp phải (`a ? b : c ? d : e`). Biên dịch thành nhánh nhảy bytecode — không tốn opcode mới.
*   **`+=` `-=` `*=` `/=`**: desugar tại parser thành `x = x + y` — tái dùng đường biên dịch sẵn có.
*   **`++` / `--`** (prefix & postfix): desugar thành `x = x ± 1`. *Lưu ý: là biểu thức, nó trả về giá trị MỚI (khác JS trả giá trị cũ với postfix); dùng như câu lệnh độc lập (`i++;`) thì hành vi giống hệt JS.*

### Triết lý: những gì bị loại bỏ CÓ CHỦ ĐÍCH
Kitwork chọn sự đơn giản và an toàn vận hành. Các từ khóa sau bị **từ chối ngay khi biên dịch** kèm thông báo hướng dẫn thay thế:

| Từ khóa | Lý do loại bỏ | Cách viết thay thế |
| :--- | :--- | :--- |
| `while`, `do` | Chặn vòng lặp vô tận từ gốc | `.map()` / `.filter()` / `.find()` |
| `try`, `catch`, `finally`, `throw` | Đơn giản hóa luồng lỗi | `.done(cb)` / `.fail(cb)` |
| `switch` | Giữ ngôn ngữ tối giản | `if / else` hoặc object map |
| `class` | Không dùng OOP kế thừa | object literal + arrow function |

Ví dụ thông báo lỗi: `assemble error: Kitwork không hỗ trợ vòng lặp 'while' (loại bỏ có chủ đích để tránh vòng lặp vô tận). Hãy dùng .map() / .filter() / .find() trên mảng dữ liệu.`

### `Math`
Hằng số: `PI`, `E`, `LN2`, `LN10`, `LOG2E`, `LOG10E`, `SQRT2`, `SQRT1_2`.
Hàm: `abs`, `floor`, `ceil`, `round` (chuẩn JS: làm tròn .5 lên), `trunc`, `sign`, `sqrt`, `cbrt`, `pow`, `exp`, `log`, `log2`, `log10`, `sin`, `cos`, `tan`, `asin`, `acos`, `atan`, `atan2`, `hypot`, `min(...)`, `max(...)`, `random()`.

### `Date`
```javascript
Date.now()                    // epoch milliseconds
Date.parse("2026-06-12")      // epoch ms từ chuỗi ngày
Date.UTC(2026, 5, 12)         // epoch ms theo UTC (month tính từ 0)

const d = new Date();          // thời điểm hiện tại
const x = new Date(1749720000000);      // từ epoch ms
const y = new Date("2026-06-12");       // từ chuỗi (RFC3339, YYYY-MM-DD, ...)
const z = new Date(2026, 5, 12, 8, 30); // year, monthIndex, day, h, m, s, ms

d.getTime(); d.getFullYear(); d.getMonth(); d.getDate(); d.getDay();
d.getHours(); d.getMinutes(); d.getSeconds(); d.getMilliseconds();
d.getUTCFullYear(); d.getTimezoneOffset();
d.toISOString(); d.toJSON(); d.toString(); d.toLocaleDateString();
```

Cơ chế bên dưới: `Date` là một `value.FuncObject` — hàm kèm thuộc tính tĩnh, mô phỏng "function là object" của JS. Xem `work/globals.go` và test chuẩn tại `work/jscompat_test.go`.

### String methods (chuẩn JS, an toàn Unicode)

Tất cả chỉ số tính theo **ký tự (rune)** — `"Phường".length === 6`, `slice/charAt` không bao giờ cắt vỡ ký tự tiếng Việt:

```javascript
"hello world".slice(6)            // "world"   (hỗ trợ chỉ số âm)
"hello".substring(3, 1)           // "el"      (tự hoán đổi như JS)
"Phường Bến Nghé".indexOf("Bến")  // 7         (chỉ số rune, có fromIndex)
s.lastIndexOf(x)  s.charAt(i)  s.charCodeAt(i)  s.at(-1)
"ab".repeat(3)                    // "ababab"  (chặn kết quả > 8MB — bảo vệ multi-tenant)
"5".padStart(3, "0")              // "005"     · padEnd tương tự
s.trim()  s.trimStart()  s.trimEnd()
"a-a-a".replace("a", "b")         // "b-a-a"   (CHỈ lần đầu — đúng chuẩn JS)
"a-a-a".replaceAll("a", "b")      // "b-b-b"
s.split("-")  s.split()           // split() không đối số → [s] (chuẩn JS)
s.concat(x, y)  s.includes(x)  s.startsWith(x)  s.endsWith(x)
s.toUpperCase()  s.toLowerCase()  s.capitalize()  // capitalize là mở rộng Kitwork
```

⚠️ **Thay đổi hành vi (v1.6)**: trước đây `replace` thay *tất cả* — nay theo đúng chuẩn JS chỉ thay *lần đầu*; dùng `replaceAll` để thay tất cả.

### Array methods (chuẩn JS)

Cùng với `map` / `filter` / `find` sẵn có, engine hỗ trợ đầy đủ:

```javascript
// Callback methods (thực thi Lambda trong VM):
nums.forEach(x => { sum += x; });
nums.some(x => x > 4);        nums.every(x => x > 0);
nums.findIndex(x => x === 4);
nums.reduce((acc, x) => acc + x, 0);   // initial value tùy chọn
items.sort((a, b) => b - a);           // comparator — sắp xếp tại chỗ như JS

// Non-callback:
a.slice(1, 3)      // mảng MỚI, hỗ trợ chỉ số âm
a.indexOf(x)  a.lastIndexOf(x)  a.includes(x)   // so sánh deep-equal
a.concat(b, 5)     // mảng MỚI, đối số mảng được trải phẳng một cấp
[1,[2,[3]]].flat(2)
a.join("-")  a.push(x)  a.pop()  a.shift()  a.unshift(x)
a.reverse()  a.unique()  a.compact()  a.shuffle()  a.random()  // mở rộng Kitwork
```

⚠️ **Lệch chuẩn có chủ đích — `sort()` không comparator**: JS mặc định ép phần tử thành chuỗi (`[10, 2].sort()` → `[10, 2]` — footgun nổi tiếng). Kitwork chọn hành vi hợp trực giác: mảng toàn số xếp theo giá trị số tăng dần (`[2, 4, 10, 33]`), còn lại xếp theo chuỗi.

### Lexical scoping nhiều cấp (v1.6)

Closure lồng nhau ở **mọi độ sâu** đều đọc/ghi được biến của các hàm bao ngoài — đúng ngữ nghĩa scope chain của JS (trước v1.6 chỉ capture được 1 cấp):

```javascript
const search = (query) => {
    const results = [];                  // biến của hàm bao ngoài
    keys.forEach((key) => {
        groups[key].forEach((item) => {  // lambda cấp 2 vẫn thấy results
            if (item.indexOf(query) != -1) results.push(item);
        });
    });
    return results;
};
```

Cơ chế: mỗi Lambda giữ con trỏ `Parent` tới closure bao ngoài; `LOAD`/`STORE` leo chuỗi scope (cục bộ → chuỗi closure → top-level → Globals).

### Object / Number / String / Boolean globals

```javascript
Object.keys(o)  Object.values(o)  Object.entries(o)   // ⚠️ thứ tự key KHÔNG đảm bảo (Go map) — cần ổn định hãy .sort()
Object.assign(target, src1, src2)   // mutate target, trả về target (chuẩn JS)
Object.fromEntries(pairs)

Number("42.5")        // 42.5 · chuỗi không phải số → null (JS trả NaN, VM không có NaN)
Number.parseInt("99.9")   Number.parseFloat(s)
Number.isInteger(7)       Number.isFinite(x)
Number.MAX_SAFE_INTEGER   Number.MIN_SAFE_INTEGER   Number.EPSILON
(3.14159).toFixed(2)      // "3.14" — trả CHUỖI như JS

String(123)               // "123"
String.fromCharCode(75, 105, 116)   // "Kit"
Boolean(x)                // truthiness chuẩn JS
```

---
*Tài liệu này được biên soạn cho Kitwork Engine v1.5.0+*

