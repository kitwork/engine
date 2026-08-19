# Đánh giá tổng thể

> **Historical review snapshot.** Bản đánh giá này ghi lại trạng thái của
> KitJS `0.8.0` trước khi `data-kit-scope`, `data-kit-ignore`, server expression
> preflight và bộ conformance browser hiện tại được hoàn thiện. Nó được giữ
> lại như bằng chứng kiến trúc, không phải roadmap hay release-readiness
> hiện hành. Finding về model `$` đã được đóng trong `0.9.0-next.1`. Quyết
> định tạm thời loại `?.` ở bản đó sau này được thay bằng contract rõ hơn:
> ordinary access là strict và optional chaining có semantics riêng. Vì vậy
> các khuyến nghị phía dưới vẫn là lịch sử của snapshot, không phải contract
> hiện hành.

Sau khi đọc mã nguồn của parser, evaluator, component lifecycle, directives, structural rendering, event system, Morph/Drive, script build và test—not chỉ README—tôi đánh giá **KitJS 0.8.0 là một browser runtime có kiến trúc nghiêm túc và bản sắc riêng**.

Nó không còn là một thư viện nhỏ để thêm vài directive vào HTML. Nó đã trở thành:

> **Một HTML-first browser runtime có ngôn ngữ biểu thức đóng, component boundary, keyed reconciliation và tùy chọn duy trì điều hướng.**

Điểm quan trọng là: KitJS **tối giản về bề mặt và khái niệm**, không tối giản về số byte. Public API chỉ có `kit.version` và `kit.component()`, nhưng bản Kit có 115.211 byte và Hydrate có 157.981 byte mã nguồn đọc được. Vì vậy nên định vị nó là **semantic minimalism**, không nên quảng bá như một “tiny microframework”. ([GitHub][1])

| Hạng mục                            | Đánh giá |
| ----------------------------------- | -------: |
| Triết lý và định vị                 |     9/10 |
| Tính nhất quán của API              |     9/10 |
| Kiến trúc an toàn                   |   8.5/10 |
| Build và đóng gói                   |     9/10 |
| Khả năng bảo trì                    |     8/10 |
| Test semantics runtime              |     5/10 |
| Bằng chứng production/cross-browser |   4.5/10 |
| Tổng thể về thiết kế                | **8/10** |
| Mức sẵn sàng production hiện tại    | **6/10** |

## Những gì KitJS đang làm rất đúng

### 1. Public API thực sự nhỏ

Hai base profile chỉ công khai:

```js
kit.version
kit.component(name, plainObject)
```

Không có public compiler, manual mount, destroy, render, plugin registry, service locator hay navigation object. Đây là một quyết định rất tốt vì nó ngăn runtime dần biến thành một framework tổng hợp với hàng chục extension point khó giữ tương thích. ([GitHub][1])

Component cũng có mô hình dễ hiểu:

* Plain object được snapshot.
* State phản ứng ở mức nông.
* Mỗi component là một render boundary.
* Nhiều lần ghi trong cùng nhịp được gộp thành một microtask render.
* Không có effects, signals, watchers hay virtual DOM. ([GitHub][1])

Đây là kiểu tối giản đúng với KitJS: **ít cơ chế ngầm, ít API, ít ma thuật**.

Tôi sẽ giữ nguyên triết lý shallow reactivity. Không nên thêm deep proxy chỉ để hỗ trợ:

```js
this.items.push(item)
```

Nên tiếp tục yêu cầu:

```js
this.items = [...this.items, item]
```

Quy tắc này đơn giản, đoán trước được và tránh một hệ thống dependency tracking phức tạp.

---

### 2. Kỷ luật build và release rất tốt

Phần đóng gói là một trong những điểm mạnh nhất của repo:

* Hai artifact công khai có kích thước và SHA-256 được khóa chính xác.
* Build có chế độ kiểm tra để xác nhận source sinh đúng distribution.
* Không có runtime dependency, optional dependency, peer dependency hay install hook.
* `npm pack --dry-run` được kiểm tra để khóa chính xác bề mặt package.
* Hai bản phân phối readable là artifact chuẩn, không tồn tại một bản minified có thể vô tình trở thành implementation thứ hai. 

Đây không chỉ là “build chạy được”. Nó tạo ra một chuỗi phân phối có tính quyết định:

```text
source → profile graph → exact bytes → exact hash → package surface
```

Đối với một runtime muốn hướng đến closed artifact, exact component graph và SRI, cách làm này rất phù hợp.

Việc tách riêng:

```text
kit.js
hydrate.kit.js
```

cũng đúng hơn việc nhét Morph/Drive vào base runtime rồi bật tắt bằng cấu hình. Hai profile có hợp đồng khác nhau, vì vậy nên tiếp tục là hai artifact riêng biệt. ([GitHub][1])

---

### 3. Expression language được thiết kế an toàn từ nền móng

Đây là phần khiến KitJS khác rõ nhất với các thư viện directive nhỏ.

Expression không được đưa vào `eval()` hoặc `Function`. Evaluator:

* Giới hạn 10.000 node visits.
* Giới hạn 64 nested calls.
* Chỉ đọc own-property.
* Chặn các tên có khả năng thoát ra prototype/global.
* Dùng null-prototype object.
* Chỉ cho phép một danh sách method hữu hạn.
* Chỉ cho action ghi vào top-level field đã tồn tại.
* Staging các assignment và chỉ commit nếu toàn bộ synchronous action thành công. 

Component definitions cũng được snapshot chặt chẽ: chặn symbol field, `$` namespace, circular state và object có prototype không phù hợp. Instance được tạo từ `Object.create(null)`. 

Đây là một lựa chọn rất đúng:

> HTML expression là ngôn ngữ không tin cậy; component JavaScript mới là mã ứng dụng có quyền đầy đủ.

Việc `$event` chỉ là frozen scalar snapshot thay vì native event cũng làm giảm đáng kể bề mặt truy cập ngoài ý muốn. ([GitHub][1])

Tôi chưa xem đây là “đã chứng minh an toàn”, vì custom parser, evaluator và HTML morph đều cần test đối kháng sâu hơn. Nhưng **hướng kiến trúc là đúng**.

---

### 4. Hydrate có hợp đồng tương thích rất chặt

Hydrate không đơn giản chỉ là `fetch()` rồi thay `body`.

Trước khi làm thay đổi tài liệu hiện tại, nó kiểm tra:

* Cùng profile và resolved script URL.
* Component trên trang mới đã được biết.
* Retain graph tương thích.
* Base URL tương thích.
* Không chứa active document content.
* Closed graph/version phải khớp nếu manifest tồn tại.

Chỉ sau khi toàn bộ kiểm tra hoàn tất nó mới thay history, head và body; nếu không tương thích, nó quay về browser navigation. 

Fetched scripts không được thực thi. Focus, scroll, dirty form state, history và retained component được giữ lại nơi an toàn. Đây là thiết kế “progressive enhancement thật sự”: Hydrate thất bại thì trình duyệt vẫn điều hướng bình thường, thay vì để trang ở trạng thái nửa cũ nửa mới. ([GitHub][1])

Phần này có độ chín về tư duy cao hơn nhiều runtime nhỏ.

# Vấn đề cần sửa ngay

## 1. `?.` hiện không có ý nghĩa khác với `.`

Đây là sai lệch contract rõ nhất tôi thấy.

Spec công bố cả hai dạng:

```text
.name
?.name
```

Parser cũng ghi lại thuộc tính `chain: true` khi gặp `?.`. ([GitHub][2])

Nhưng evaluator của member access không kiểm tra `ast.chain`. Nó luôn làm:

```js
var owner = evaluate(ast.object, resolver, budget);
if (owner === null || owner === undefined) return undefined;
```

Do đó, theo mã hiện tại:

```js
user.name
user?.name
```

đều trả về `undefined` khi `user` nullish. Đây là suy luận trực tiếp từ parser và evaluator hiện tại. 

Tôi không nghĩ nên mở rộng evaluator để bắt chước hoàn toàn JavaScript optional chaining. Với triết lý của KitJS, lựa chọn đơn giản hơn là:

> **Bỏ `?.` khỏi grammar và xác định rõ mọi member access bằng `.` đều null-safe.**

Như vậy:

```html
<span data-kit-text="user.name"></span>
```

đã đủ an toàn. Không cần giữ hai cú pháp nhưng chỉ có một semantics.

Trường hợp muốn `.` phải strict thì cần implement sự khác biệt thật sự và bổ sung test cho:

```js
a.b
a?.b
a?.b.c
a?.b()
a.b()
```

Hiện trạng không nên giữ vì người đọc spec sẽ kỳ vọng hai toán tử có ý nghĩa khác nhau.

---

## 2. `data-kit-model` cho phép cú pháp field bắt đầu bằng `$`, nhưng component lại cấm

`model.js` hiện nhận:

```js
/^[A-Za-z_$][A-Za-z0-9_$]*$/
```

Trong khi component definitions cấm mọi field bắt đầu bằng `$`. Kết quả là `$name` vượt qua bước parse model rồi chỉ thất bại muộn khi tìm writable field. 

Nên đổi thành:

```js
/^[A-Za-z_][A-Za-z0-9_]*$/
```

Đây là lỗi nhỏ, nhưng việc fail sớm và đúng thông báo rất phù hợp với triết lý fail-closed của KitJS.

# Điểm yếu lớn nhất: test đang khóa package tốt hơn khóa runtime

Repo hiện có ba nhóm test:

```text
browser-smoke.test.mjs
dist-parity.test.mjs
package-contract.test.mjs
```

Browser smoke test kiểm tra hai profile boot hai lần, xung đột profile và một counter cơ bản. Hai test còn lại khóa exact artifact, hash, manifest và nội dung package. 

Đây là các test release rất tốt. Nhưng chúng chưa chứng minh phần khó nhất của KitJS hoạt động đúng.

Trước khi xem xét `1.0`, runtime cần test chuyên biệt cho ít nhất các nhóm sau:

| Khu vực      | Trường hợp cần khóa                                                  |
| ------------ | -------------------------------------------------------------------- |
| Lexer/parser | precedence, nesting limit, malformed input, forbidden syntax         |
| Evaluator    | own-property, blocked names, method allowlist, call ownership        |
| Transaction  | rollback toàn bộ khi expression cuối thất bại                        |
| Component    | init, cleanup, detach, nested boundaries, duplicate registration     |
| Model        | checkbox array, radio, select multiple, number, range, IME           |
| Events       | once, prevent, stop, outside, debounce, enter/escape                 |
| Structure    | keyed reuse, move, duplicate key, removal, nested `if`/`for`         |
| Alias        | missing, duplicate, detached, action-only                            |
| Async        | Promise settlement sau khi component bị tháo khỏi DOM                |
| Hydrate      | focus, scroll, dirty form, history, retain, fallback-before-mutation |
| Security     | prototype paths, DOM clobbering, URL schemes, SVG, malformed HTML    |

Đặc biệt, nên có **differential test cho parser/evaluator**: cùng một expression chạy qua các trạng thái khác nhau và kiểm tra cả result, write set, rollback và error type.

Không cần hàng nghìn test ngay lập tức. Nhưng khoảng 100–200 test nhỏ, tập trung vào contract, sẽ giá trị hơn việc thêm directive hoặc service mới.

# Các điểm nóng hiệu năng cần đo trước khi mở rộng

Tôi thấy một số đường chạy có độ phức tạp theo toàn bộ document:

* Promise settlement duyệt mọi `[data-kit-component]` để tìm observation ticket.
* Alias resolution duyệt mọi `[data-kit-as]`.
* `outside` event duyệt `document.querySelectorAll("*")`.
* Debounced event duyệt toàn bộ element để tìm lại owner đang kết nối. 

Đây không nhất thiết là thiết kế sai. Nó đang đánh đổi index toàn cục lấy:

* Ít strong reference.
* Giảm nguy cơ giữ detached DOM.
* Connected DOM tiếp tục là source of truth.
* Cleanup đơn giản hơn.

Với trang vài trăm hoặc vài nghìn node, cách này có thể hoàn toàn ổn. Nhưng `outside` nằm trên đường chạy event thường xuyên, còn Promise settlement có thể tăng mạnh trong ứng dụng lớn.

Tôi chưa khuyên viết lại các đoạn này. Nên thêm benchmark trước:

```text
1.000 / 10.000 / 50.000 DOM nodes
100 / 1.000 component boundaries
10 / 100 outside handlers
100 / 1.000 keyed rows
100 Promise settlements cùng lúc
```

Chỉ khi có dữ liệu xấu mới cân nhắc:

* Cache alias theo generation của MutationObserver.
* Registry theo từng outside event type.
* Ticket → weak owner mapping.
* Hủy debounce rõ ràng khi element bị dispose.

Đừng đánh đổi mô hình ownership sạch hiện tại để lấy một tối ưu chưa được đo.

# Hydrate cần được xem như một subsystem riêng

Hydrate thêm khoảng 43 KB readable code so với profile cơ bản. Điều đó không đáng lo về bản thân kích thước, nhưng nó cho thấy Morph/Drive đã là một subsystem thực sự chứ không còn là một tiện ích nhỏ. 

Nó cần release gate riêng cho:

* Navigation compatibility.
* Form and focus preservation.
* History back/forward.
* Redirects và aborted requests.
* Retained component lifecycle.
* Head reconciliation.
* Script non-execution.
* Cross-browser DOM behavior.

Hiện README nói runtime được chạy liên tục bằng Chromium harness, còn legacy browser không được cam kết. Điều này hợp lý ở `0.8`, nhưng Hydrate nên sớm được chạy trên Chromium, Firefox và WebKit bởi DOMParser, focus, form state, history và selection có nhiều khác biệt giữa engine. ([GitHub][1])

Tôi sẽ **giữ Kit và Hydrate tách biệt**, không hợp nhất thành một file có cờ cấu hình.

# Security cần test đối kháng, không chỉ thiết kế tốt

URL sanitizer hiện dùng denylist cho:

```text
javascript:
vbscript:
data:text/html
```

sau khi xóa ASCII control/space và lowercase. Morph cũng loại event attributes, `srcdoc` và active embedded-document elements. 

Tôi chưa thấy một lỗ hổng cụ thể từ đoạn này. Tuy nhiên cần test corpus đối kháng cho:

```text
HTML entities
mixed case
Unicode whitespace
SVG/xlink namespaces
data: variants
blob:
percent-encoded inputs
malformed attributes
DOM clobbering
mutation-XSS patterns
```

Tương tự, expression evaluator nên được fuzz bằng tên property, object shape, nesting và các combination của lambda/method/member access.

Một runtime tự có parser và DOM morph phải xem fuzzing như một phần của contract, không phải việc làm thêm khi gần `1.0`.

# Bề mặt package và tài liệu

README rất trung thực khi nói hai base npm file không chứa optional services và `0.8.0` chưa cung cấp public build interface để tạo custom closed artifact. Tuy nhiên, việc catalog hàng loạt service ngay trong README chính có thể khiến người dùng tưởng chúng có sẵn trong package vừa cài. ([GitHub][1])

Tôi sẽ chia tài liệu thành hai tầng:

```text
KitJS Base
├── kit.js
├── hydrate.kit.js
├── component
├── directives
└── expression language

KitJS Closed Integration
├── exact component graph
├── optional sealed services
├── private manifest
└── Kitwork build adapter
```

README chính nên tập trung vào thứ người dùng có thể sử dụng ngay. Closed graph và service catalog nên nằm trong `docs/closed-artifacts.md` hoặc `INTEGRATION.md`.

Ngoài ra hiện có bốn cách gọi:

```text
Sản phẩm:     KitJS
npm package:  @kitwork/kitjs
repository:   kitwork/kit.js
global:       kit
```

Không cần đổi tên, nhưng nên có một dòng mapping rõ ràng gần đầu README để tránh người dùng nghĩ `kit.js` và KitJS là hai dự án khác nhau.

# Độ trưởng thành hiện tại

Public repository hiện chỉ hiển thị bảy commit; bản standalone `0.8.0` được chuẩn bị và chuyển sang Kitwork scope vào ngày 11 tháng 8 năm 2026. Điều này không có nghĩa code chỉ mới tồn tại một ngày—nó có thể đã được tách ra từ Kitwork Engine—nhưng public package chưa tích lũy nhiều lịch sử phát hành và bằng chứng sử dụng bên ngoài. ([GitHub][1])

Trang Actions tại thời điểm tôi kiểm tra hiển thị `0 workflow runs`, dù chính giao diện GitHub cũng báo lỗi tải một số phần. Vì vậy nên bảo đảm test suite thực sự chạy như required checks trên mỗi push và release, thay vì chỉ tồn tại dưới dạng script trong repo. ([GitHub][3])

Việc package đang mặc định publish bằng tag `next` là quyết định đúng cho giai đoạn này. 

# Thứ tự phát triển tôi đề xuất

| Mốc       | Việc nên làm                                                                                   |
| --------- | ---------------------------------------------------------------------------------------------- |
| **0.8.1** | Quyết định semantics của `.`/`?.`; sửa model `$`; thêm parser/evaluator transaction tests      |
| **0.8.x** | Test đầy đủ directive, lifecycle, keyed loop, alias, async cleanup                             |
| **0.9.0** | Hydrate E2E trên Chromium/Firefox/WebKit; security corpus; benchmark DOM lớn                   |
| **0.9.x** | Stabilize diagnostics, error contract, debug instrumentation                                   |
| **1.0.0** | Freeze spec, compatibility policy, release CI, closed-artifact boundary và tài liệu production |

Trong giai đoạn này, tôi sẽ **không thêm directive mới, không thêm deep reactivity, không mở plugin registry và không biến generic native bridge thành một phần base runtime**.

# Kết luận cuối cùng

**Tôi sẽ tiếp tục phát triển KitJS từ nền móng hiện tại, không viết lại theo hướng framework khác.**

Những thứ nên giữ nguyên:

* HTML là source of truth.
* Component boundary là đơn vị reactive.
* Chỉ hai public API.
* Closed expression language.
* Không `eval`.
* Không VDOM.
* Không runtime dependency.
* Kit và Hydrate là hai profile riêng.
* Services chỉ xuất hiện trong closed artifact.
* Mọi thứ không tương thích đều fail trước khi mutation.

Điểm mạnh thật sự của KitJS không phải “ít code hơn Alpine” hay “nhẹ hơn framework X”. Điểm mạnh của nó là:

> **Một runtime HTML-first nhỏ về khái niệm, đóng về hành vi, quyết định về artifact và có thể được kiểm chứng từ source đến browser.**

Hiện tại kiến trúc đã đủ tốt để trở thành một dự án độc lập có giá trị. Việc quan trọng nhất bây giờ không phải thêm tính năng, mà là **chuyển các contract đang được viết rất chặt thành một test suite chặt tương đương**.

[1]: https://github.com/kitwork/kitjs "GitHub - kitwork/kit.js: KitJS is a standalone browser runtime for adding stateful behavior to ordinary HTML. It does not require Kitwork, a server runtime, a virtual DOM, a frontend compiler, or runtime package downloads. Its authored expressions use a small, closed language that is parsed without `eval()` or `Function`. · GitHub"
[2]: https://github.com/kitwork/kit.js/blob/master/KITJS_SPEC.md "kit.js/KITJS_SPEC.md at master · kitwork/kit.js · GitHub"
[3]: https://github.com/kitwork/kit.js/actions "Workflow runs · kitwork/kit.js · GitHub"
