# KITJS V2 — Agent Implementation Brief

> **Mục đích:** Tài liệu này là chỉ dẫn triển khai dành cho coding agent sửa lại KitJS theo hướng V2.  
> **Nguyên tắc cao nhất:** Không viết lại từ đầu. Kế thừa kernel hiện tại, lấy lại các semantics đã được chứng minh trong `legacy`, nhưng không mang trở lại độ phức tạp của framework cũ.

---

# 1. Vai trò của Agent

Bạn đang làm việc trên hệ sinh thái Kitwork, cụ thể là KitJS runtime.

Nhiệm vụ của bạn là:

1. Đọc code hiện tại của KitJS trong Kitwork Engine.
2. Đọc repository `kitwork/kit.js`.
3. Đọc thư mục `legacy` như tài liệu lịch sử và test corpus.
4. Xác định source-of-truth thực tế.
5. Sửa runtime theo kiến trúc V2 trong tài liệu này.
6. Giữ tương thích với cú pháp và hành vi đã tồn tại khi có thể.
7. Viết test trước hoặc cùng lúc với từng thay đổi.
8. Regenerate các artifact phân phối từ source chính thức.
9. Không sửa generated file như source chính nếu repository đã có quy trình build/generate.

Không được giả định tên file, command hoặc kiến trúc module nếu chưa kiểm tra code thực tế.

---

# 2. Mục tiêu kiến trúc

KitJS V2 không phải là một frontend framework mới.

KitJS V2 là browser execution kernel của Kitwork:

```text
KitJS V2
=
Current secure kernel
+
Legacy proven semantics
-
Legacy framework complexity
```

Mục tiêu cuối cùng:

```text
Một expression grammar
Một scope model
Một binding engine
Một event router
Một structural block engine
Một lifecycle/resource contract
Một server-client conformance contract
```

KitJS phải tiếp tục phù hợp với triết lý:

> HTML là cấu trúc. KitJS bổ sung state và behavior vào đúng nơi chúng được sử dụng.

---

# 3. Source of Truth

Agent phải xác minh lại trong repository, nhưng định hướng hiện tại là:

```text
Kitwork Engine source
└── engine/jit/hydrate/runtime.js
```

Các file trong repository package như:

```text
dist/kitjs.js
dist/kitjs.min.js
```

phải được xem là generated artifacts nếu build system hiện tại xác nhận điều đó.

## Quy tắc

- Sửa source chính trong Kitwork Engine trước.
- Không chỉ patch `dist/kitjs.js`.
- Chạy đúng build/generate script đã tồn tại trong repository.
- Nếu build pipeline chưa có kiểm tra đồng bộ, bổ sung test hoặc CI check để phát hiện generated output bị lệch source.
- Repository package và runtime được phục vụ bởi Kitwork Engine không được trở thành hai implementation độc lập.

---

# 4. Phạm vi phải giữ nguyên

Không được làm mất những năng lực đang có trong kernel hiện tại nếu chưa có quyết định loại bỏ rõ ràng.

Agent phải lập inventory chính xác trước khi sửa.

Tối thiểu cần kiểm tra và bảo toàn:

- zero-eval expression engine;
- lexer/parser/IR walker hiện tại;
- security blocklist cho property và global nguy hiểm;
- lexical scope;
- component blueprint;
- event delegation;
- `data-kit-text`;
- `data-kit-show`;
- `data-kit-model`;
- `data-kit-validate`;
- API/fetch helpers;
- SSE/live behavior;
- persistence/remember/storage;
- SPA navigation;
- DOM morph;
- keyed identity;
- `$app` bridge;
- component initialization;
- current compatibility syntax;
- build/generation flow.

Nếu một phần cần refactor, phải có regression test trước khi thay đổi.

---

# 5. Những gì lấy từ Legacy

Thư mục `legacy` không phải source để copy nguyên.

Nó là:

```text
Historical design reference
Integration test corpus
Semantics reference
Proof that features were previously useful
```

Agent cần nghiên cứu và kế thừa đúng semantics của các phần sau:

- `data-kit-ref`;
- `$refs`;
- `data-kit-item`;
- `$items` nếu còn use case thực tế;
- structural `data-kit-if`;
- structural `data-kit-for`;
- keyed item identity;
- reactive dependency ideas;
- computed/watch semantics nếu thật sự cần;
- lifecycle cleanup;
- outside interaction;
- intersection/visibility sources;
- component destroy/dispose;
- form/model behavior;
- list, paginate, loadmore, dropdown, tags và các component thực tế.

Không được copy nguyên:

- evaluator rộng của legacy;
- per-node listener architecture;
- component instance quá lớn;
- parent-child state synchronization hai chiều;
- mutation metadata trực tiếp vào item object;
- parser phụ riêng cho từng directive;
- module/version fragmentation;
- global access không an toàn.

---

# 6. Kiến trúc đích

KitJS V2 nên hội tụ về các lớp sau:

```text
KitJS V2
│
├── Expression Engine
│   ├── lexer
│   ├── parser
│   ├── IR
│   ├── safe walker
│   └── dependency extraction
│
├── Scope Engine
│   ├── page/application scope
│   ├── lexical boundaries
│   ├── component scope
│   └── child list scope
│
├── Binding Engine
│   ├── text
│   ├── show
│   ├── bind
│   ├── attr
│   ├── model
│   └── validate
│
├── Event Router
│   ├── delegated native events
│   ├── modifiers
│   ├── global targets
│   └── observer/lifecycle sources
│
├── Block Engine
│   ├── if
│   ├── for
│   ├── blueprint
│   ├── keyed instances
│   └── SSR adoption
│
├── Component Engine
│   ├── blueprint
│   ├── methods
│   ├── init
│   ├── dispose
│   ├── refs
│   └── alias
│
├── Resource Manager
│   ├── event subscriptions
│   ├── observers
│   ├── timers
│   ├── SSE/subscriptions
│   ├── pending async work
│   └── cleanup
│
└── Host Services
    ├── api
    ├── live
    ├── persistence
    ├── drive/morph
    └── $app
```

Không bắt buộc tách thành đúng số file/module trên. Đây là separation of concerns bắt buộc, không phải cấu trúc thư mục bắt buộc.

---

# 7. Biến hệ thống và tương thích

Không được đổi nghĩa biến cũ một cách âm thầm.

## Canonical V2

```text
$this   Element sở hữu directive hiện tại
$host   Nearest scope/component DOM boundary
$event  Native DOM event
$error  Error boundary context
$refs   Scoped reference registry
$app    Application/platform bridge
$       Page/application root state
```

## Compatibility aliases

```text
$el     Alias tương thích của $this
$root   Alias tương thích của $host nếu code hiện tại đang dùng $root như DOM boundary
```

## Quy tắc bắt buộc

- Không repurpose `$root` thành root state nếu code hiện tại dùng nó như DOM boundary.
- Giữ `$` làm page/application root state nếu đây là semantics hiện tại.
- `$this` không nhất thiết bằng native `event.currentTarget` khi runtime dùng event delegation.
- `$this` luôn là element chứa directive đang được thực thi.
- `$event.target` là target DOM thực tế.
- `$host` là lexical boundary gần nhất.
- `$event` chỉ dành cho DOM event.
- `$error` chỉ dành cho error boundary.

---

# 8. Scope Syntax

KitJS V2 hỗ trợ nhiều surface syntax nhưng chỉ một AST.

## Canonical object literal

```html
<div data-kit-scope="{ count: 0, open: false }">
```

## Official shorthand

```html
<div data-kit-scope="count: 0; open: false">
```

## Compatibility syntax

Nếu code hiện tại đã hỗ trợ:

```html
<div data-kit-scope="count = 0; open = false">
```

hoặc named scope:

```html
<div data-kit-scope="counter">
```

thì tiếp tục hỗ trợ trong compatibility mode.

## Parser requirements

- Không dùng `split(';')` thô.
- Phải phân cách theo token depth.
- Phải xử lý đúng string, array, object và nested expression chứa dấu `;`.
- Mọi hình dạng cuối cùng phải normalize thành cùng một scope declaration/AST.
- Không tạo expression engine thứ hai.

---

# 9. Scope Resolution

Lexical scope phải là model duy nhất.

## Read

Tìm key từ scope gần nhất rồi đi lên parent scope chain.

## Write

- Nếu key đã tồn tại, ghi vào scope sở hữu key gần nhất.
- Nếu key mới, ghi vào boundary scope hiện tại theo contract đã chốt.
- Không ghi âm thầm vào global object.
- Không đồng bộ parent/child bằng effect hai chiều như legacy.
- Không mutate state object gốc để thêm metadata runtime.

Agent phải bổ sung test cho:

- shadowing;
- parent lookup;
- nearest-owner assignment;
- new-key assignment;
- nested component;
- nested list scope;
- missing variable;
- reserved system variables.

---

# 10. Event Directives

Canonical event surface:

```text
data-kit-<event>[:<modifier>[(<argument>)]]*
```

Ví dụ:

```html
data-kit-click="open()"
data-kit-submit:prevent="save()"
data-kit-click:stop="select()"
data-kit-input:debounce(300)="search()"
data-kit-click:throttle(1000)="pay()"
data-kit-keydown:window:escape="close()"
data-kit-click:outside="close()"
data-kit-click:once="initialize()"
```

## Deterministic Pipeline Order

Runtime không phụ thuộc thứ tự modifier trong HTML.

```text
1. Resolve Target       :window, :document
2. Filter Event         :outside, :escape, :enter
3. Prevent Default      :prevent
4. Propagation Control  :stop
5. Timing Control       :debounce, :throttle
6. Lifecycle Control    :once
7. Execute Expression
```

## Bắt buộc

- Filter phải chạy trước `prevent` và `stop`.
- Nếu filter fail, event không bị nuốt.
- `prevent` và `stop` phải chạy đồng bộ trên native event trước delayed execution.
- `outside` là filter, không phải target.
- `window` và `document` là target modifiers.
- Native events tiếp tục ưu tiên delegated event router.
- Global/observer sources phải được quản lý bằng Resource Manager.

## Synthetic sources

Có thể hỗ trợ:

```html
data-kit-intersect="loadMore()"
data-kit-intersect:once="loadMore()"
data-kit-mount="init()"
```

Không gọi chúng là native DOM events.

---

# 11. Action/Behavior Compatibility

Nếu kernel hiện tại có:

```text
data-kit-action
kitwork.behavior(...)
```

thì:

- giữ behavior registry như compatibility/plugin API;
- không dùng `data-kit-action` làm canonical V2 authoring surface;
- ưu tiên native event directive và component methods;
- thêm deprecation warning trong development mode nếu phù hợp;
- không xóa ngay nếu component/application hiện tại còn phụ thuộc.

---

# 12. Binding Engine

## Text

```html
<strong data-kit-text="qty * price"></strong>
```

- ghi bằng `textContent`;
- không dùng `${expr}` trong text node;
- không render raw HTML;
- không quét text node để tìm interpolation.

## Show

```html
<div data-kit-show="open">
```

- dùng `element.hidden = !Boolean(value)`;
- giữ DOM;
- giữ scope, lifecycle và identity;
- không thêm `data-kit-hidden`.

## Property binding

Canonical:

```html
<button data-kit-bind:disabled="loading">
<input data-kit-bind:value="name">
```

### Reflected boolean

Property + attribute:

```text
disabled
required
readonly
multiple
hidden
open
```

### Live state property

Property only:

```text
checked
selected
value
indeterminate
```

## Attribute binding

```html
<div data-kit-attr:aria-expanded="open">
```

Dùng cho:

- ARIA;
- data attributes;
- custom attributes;
- attribute semantics không phải live DOM property.

## Model

```html
<input data-kit-model="user.name">
```

`model` là two-way binding và phải tồn tại song song với one-way `bind:value`.

Phải kế thừa các use case hợp lý từ legacy:

- text input;
- textarea;
- checkbox;
- radio;
- select;
- deep path;
- delegated input/change handling.

## Validation

Giữ `data-kit-validate` và semantics server/client twin.

Không hạ validation thành utility phụ.

---

# 13. Reactive Binding Registry

Không tiếp tục render bằng cách query toàn document cho từng directive sau mỗi mutation.

## Mục tiêu

Khi mount/hydrate:

1. Scan boundary một lần.
2. Compile directive expression.
3. Extract dependencies nếu có thể.
4. Tạo binding record.
5. Đăng ký binding vào scope dependency map.

Ví dụ:

```text
Binding
├── element
├── directive
├── expression IR
├── dependencies
├── target strategy
├── scope
└── resource owner
```

Khi state mutate:

1. Xác định key thay đổi.
2. Tìm affected bindings.
3. Queue update.
4. Flush một lần.

## Fallback

Nếu dependency không xác định tĩnh được:

- mark binding là dynamic;
- dirty toàn boundary hoặc scope;
- không được phá correctness để đổi lấy optimization.

## Transaction

Mỗi synchronous expression chạy trong một reactive transaction:

```text
Begin
Execute
Collect changes
Commit
Flush bindings once
Run post-update work
```

Với async, mỗi đoạn synchronous trước/sau `await` là transaction riêng.

---

# 14. Component Engine

Component instance V2 phải nhỏ.

```text
ComponentInstance
├── element
├── scope
├── blueprint
├── refs
├── resources
└── lifecycle state
```

Compiler, event router, binding engine và block engine là service chung.

Không nhét toàn bộ runtime logic vào từng component instance.

## Component syntax

Canonical:

```html
<div
  data-kit-component="modal"
  data-kit-scope="{ open: false }"
>
```

## Alias

```html
<div data-kit-alias="$paymentModal">
```

## Ref

```html
<div data-kit-ref="paymentModal">
```

## Same instance

```js
$paymentModal === $refs.paymentModal
```

Alias và ref thuộc hai namespace khác nhau.

## Compatibility shorthand

Nếu hiện tại có:

```html
data-kit-component="modal=$paymentModal"
```

thì normalize thành:

```html
data-kit-component="modal"
data-kit-alias="$paymentModal"
```

Không xóa compatibility syntax ngay.

## Collision rules

- Alias không được trùng alias trong cùng app namespace.
- Ref không được trùng ref trong cùng component/app namespace theo contract đã chọn.
- Alias và ref có thể cùng tên nếu cùng trỏ đến một instance.
- Reserved system names không được dùng làm alias.

---

# 15. Lifecycle và Resource Manager

Lifecycle tối thiểu:

```text
create
mount
update
dispose
```

Không nhất thiết expose mọi lifecycle thành directive.

Mỗi component, structural block và list item phải có resource owner.

```text
Resources
├── delegated/global event handles
├── observers
├── timers
├── effects/bindings
├── SSE/subscriptions
├── pending async work
├── refs
└── aliases
```

## Dispose requirements

Khi dispose:

- unregister ref;
- unregister alias;
- remove global listener;
- disconnect observer;
- clear timer;
- unsubscribe SSE/live subscription;
- remove binding records;
- dispose child scope;
- invalidate pending async write;
- cleanup nested blocks/components.

MutationObserver chỉ là fallback khi DOM bị thay đổi từ bên ngoài.

Khi chính KitJS remove node, KitJS phải dispose trực tiếp.

---

# 16. Structural Block Engine

Không viết riêng hai engine hoàn toàn khác nhau cho `if` và `for`.

Dùng chung một Block Engine:

```text
Block
├── id
├── start marker
├── end marker
├── blueprint
├── instances
├── child scopes
├── keys
└── resources
```

## `data-kit-for`

Source:

```html
<li
  data-kit-for="item, index of items"
  data-kit-key="item.id"
>
  <span data-kit-text="item.name"></span>
</li>
```

## Child scope

Không mutate item object.

Child scope overlay:

```text
item
index
count
first
last
even
odd
```

## `data-kit-item`

`data-kit-item` là identity của materialized DOM item.

SSR output đề xuất:

```html
<!--kit-for:start id=items-1-->

<li data-kit-item="a">
  ...
</li>

<li data-kit-item="b">
  ...
</li>

<!--kit-for:end id=items-1-->
```

Semantics:

```text
data-kit-for  Source declaration
data-kit-key  Key expression trong source
data-kit-item Stable identity trên materialized DOM item
```

Không dùng:

```text
data-kit-for-instance
data-kit-for-item
```

Không bắt buộc giữ `data-kit-for` trên từng SSR instance.

## SSR membership

Loop membership được xác định bằng marker + compiled IR/blueprint.

`data-kit-item` không cần chứa loop ID nếu marker và IR đã xác định block membership.

## Empty SSR list

```html
<!--kit-for:start id=items-1-->
<!--kit-for:end id=items-1-->
```

Blueprint vẫn phải tồn tại trong compiled IR hoặc hydration metadata.

## Blueprint

Không lấy live hydrated DOM item đầu tiên làm prototype.

Blueprint phải đến từ:

1. compiled server IR; hoặc
2. immutable pre-hydration clone đã strip live state.

Ưu tiên compiled IR.

## Reconciliation

Phải hỗ trợ:

- initial SSR adoption;
- insert;
- remove;
- append;
- prepend;
- reorder;
- stable DOM identity;
- duplicate-key error;
- nested lists;
- cleanup;
- interaction với SPA morph;
- focus preservation khi có thể.

## Shared key system

Hợp nhất key resolver giữa list reconciliation và morph.

Không xây hai identity engines riêng.

---

# 17. `data-kit-if`

`data-kit-if` có semantics khác `data-kit-show`.

```text
show = giữ DOM, đổi visibility
if   = mount/unmount subtree
```

Source:

```html
<section data-kit-if="editing">
  ...
</section>
```

Phải được triển khai bằng Block Engine dùng chung với `for`.

Khi false:

- dispose subtree;
- remove mounted nodes;
- giữ blueprint và marker.

Khi true:

- instantiate blueprint;
- create child scope nếu cần;
- bind subtree;
- mount resources.

Không cần thêm trong V2:

```text
data-kit-switch
data-kit-case
data-kit-default
data-kit-hidden
```

`switch/case` không tạo semantics mới.

`hidden` chỉ đảo nghĩa `show`.

Có thể hoãn `else/else-if` cho tới khi branch grouping có use case thật.

---

# 18. Error Boundary

Canonical:

```html
<div data-kit-error="handleError($error)">
```

`$error` contract tối thiểu:

```text
$error.cause
$error.directive
$error.element
$error.scope
$error.component
$error.phase
```

`$event` không được tái sử dụng làm error context.

Development mode phải cung cấp lỗi có ngữ cảnh:

```text
Component
Directive
Expression
Element
Scope path
Cause
Runtime phase
```

Production mode không làm sập toàn bộ app khi một island lỗi nếu có thể cô lập.

---

# 19. Async Contract

Inline grammar không cần `await`.

HTML gọi method:

```html
<button data-kit-click="scanQRCode()">
```

Method có thể async:

```js
async function scanQRCode() {
  loading = true
  try {
    scannedToken = await $app.qrcode.scan()
  } finally {
    loading = false
  }
}
```

Runtime requirements:

- expression có thể trả Promise;
- không block event loop;
- Promise rejection đi vào error boundary;
- state updates sau async vẫn reactive;
- disposed scope không nhận late async write;
- cancellation/abort được hỗ trợ ở method hoặc host capability layer;
- `prevent` và `stop` chạy đồng bộ trước Promise.

---

# 20. Security Requirements

Không được làm suy yếu zero-eval model.

Bắt buộc giữ hoặc tăng cường:

- no `eval`;
- no `new Function`;
- block prototype pollution paths;
- block dangerous globals;
- controlled method/property access;
- no arbitrary global fallback;
- reserved variable protection;
- safe bridge invocation;
- scope isolation;
- capability boundary cho `$app`.

Nếu legacy có behavior tiện lợi nhưng yêu cầu mở rộng global access, không mang behavior đó trở lại core.

---

# 21. `$app` Capability Boundary

`$app` không chỉ là helper.

Nó là host capability boundary.

Ví dụ capability:

```text
camera.read
qrcode.scan
clipboard.write
storage.read
storage.write
http.fetch
```

Agent không cần xây permission system hoàn chỉnh trong một lần nếu hiện chưa có, nhưng kiến trúc mới không được đóng cứng bridge thành object global không kiểm soát.

Mọi capability call phải có đường mở rộng cho:

- permission;
- tenant context;
- audit;
- cancellation;
- host adapter;
- error normalization.

---

# 22. Build Profiles

Một source-of-truth có thể generate nhiều profile.

Đề xuất:

```text
kitjs.core
├── expression
├── scope/component
├── events
├── text/show/bind/model
├── validate
├── refs
└── lifecycle

kitjs.structural
├── block engine
├── if
├── for
└── item

kitjs.data
├── api
├── live
└── persistence

kitjs.drive
├── navigation
├── morph
├── history
└── head merge

kitjs.platform
└── $app bridge

kitjs.full
└── all profiles
```

Không bắt buộc hoàn thành profile splitting trong cùng PR nếu build system chưa sẵn sàng.

Nhưng module boundaries mới không được làm cho việc tách profile sau này trở nên khó hơn.

---

# 23. Compatibility Matrix

Agent phải tạo hoặc cập nhật một compatibility matrix chính thức.

Tối thiểu:

```text
Legacy/current syntax                V2 behavior
────────────────────────────────────────────────────────
$el                                  $this canonical + $el alias
$root as DOM boundary                $host canonical + $root alias
$ as root state                      giữ nguyên
data-kit-action                      compatibility/plugin surface
kitwork.behavior(...)                compatibility/plugin registry
data-kit-component="x=$alias"        normalize thành component + alias
data-kit-ref                         hỗ trợ chính thức
data-kit-item                        materialized item identity
data-kit-if                          Block Engine
data-kit-for                         Block Engine
data-kit-show                        hidden property
data-kit-model                       two-way binding
data-kit-bind:*                      one-way property binding
data-kit-validate                    giữ server/client twin
```

Mỗi compatibility behavior cần test.

---

# 24. Legacy as Test Corpus

Không chạy legacy runtime trong production.

Chuyển các use case legacy thành fixtures/integration tests:

- counter;
- todo;
- list;
- dropdown;
- tags;
- form;
- paginate;
- loadmore;
- sidebar;
- QR login nếu phù hợp;
- nested component;
- nested list;
- outside event;
- intersect;
- cleanup after removal.

Mỗi use case cần được viết lại bằng canonical V2 surface hoặc compatibility mode tùy mục tiêu test.

---

# 25. Conformance Suite

Go evaluator và KitJS evaluator phải chạy cùng fixtures.

Tối thiểu:

- literals;
- arrays;
- objects;
- arithmetic;
- comparison;
- boolean logic;
- truthiness;
- property access;
- safe property access;
- method calls;
- assignment;
- lexical resolution;
- missing variable;
- reserved variables;
- error semantics;
- array/list behavior;
- string conversion;
- null behavior.

Nếu semantics khác nhau, CI phải fail.

---

# 26. Hydration Contract

Nguyên tắc:

> Server Content First, Client Reactive State Ownership.

Khi boot:

1. Giữ DOM SSR hiện tại.
2. Adopt marker, component, binding và list instance.
3. Bind client scope state.
4. Detect mismatch.
5. Client state trở thành reactive source-of-truth sau hydration.
6. Mutation từ bất kỳ nguồn hợp lệ nào cập nhật DOM.

Mutation sources gồm:

- user event;
- async fetch;
- SSE;
- timer;
- lifecycle;
- component khác;
- `$app` bridge.

## Mismatch

Development:

```text
[KitJS Hydrate Warning]
```

kèm:

- element;
- directive;
- server value;
- client value;
- scope path;
- component;
- block/item identity.

Production policy phải được định nghĩa rõ, không để DOM và state mâu thuẫn vô thời hạn.

---

# 27. Version Contract

Nếu chưa có, bổ sung runtime/hydration/IR version metadata.

Ví dụ:

```html
<html
  data-kit-runtime="2"
  data-kit-ir="2"
>
```

Hoặc bootstrap metadata tương đương.

Client phải phát hiện:

- runtime version mismatch;
- unsupported IR;
- unsupported hydration schema;
- stale cached bundle.

Không cần dùng đúng format trên nếu hệ thống hiện tại đã có build/version metadata phù hợp.

---

# 28. Size Budget

Không dùng số đo thủ công trong tài liệu.

Build phải đo artifact thực tế.

Baseline hiện tại cần được Agent đo lại.

Budget mục tiêu:

```text
Core kernel <= 12KB gzip
```

Nếu vượt budget:

- báo CI failure hoặc explicit review;
- xuất size diff;
- chỉ rõ module tăng kích thước.

Không được giảm correctness hoặc security chỉ để đạt con số.

---

# 29. Thứ tự triển khai

Không làm tất cả trong một thay đổi khổng lồ nếu repository không phù hợp.

## Phase 0 — Audit

- xác nhận source-of-truth;
- map current architecture;
- map current public API;
- map generated artifacts;
- chạy test hiện tại;
- đo bundle;
- xác nhận README và code có lệch nhau không.

## Phase 1 — Semantics and Compatibility

- system variable aliases;
- ref/alias registry;
- collision rules;
- error context;
- event modifier parser/pipeline;
- scope syntax normalization;
- compatibility tests.

## Phase 2 — Binding Registry

- mount-time scanning;
- binding records;
- dependency extraction;
- scoped invalidation;
- transaction/flush;
- current directive migration.

## Phase 3 — Resource Manager

- resource ownership;
- dispose contract;
- global listener cleanup;
- observer cleanup;
- SSE cleanup;
- pending async invalidation.

## Phase 4 — Block Engine

- marker abstraction;
- blueprint abstraction;
- `data-kit-for`;
- `data-kit-item`;
- keyed reconciliation;
- SSR adoption;
- nested blocks;
- cleanup.

## Phase 5 — `data-kit-if`

- reuse Block Engine;
- mount/unmount semantics;
- SSR adoption;
- cleanup;
- no separate structural runtime.

## Phase 6 — Build and Profiles

- regenerate dist;
- size check;
- optional profile outputs;
- version metadata;
- documentation synchronization.

---

# 30. Test Requirements

Mỗi thay đổi phải có unit/integration test phù hợp.

## Expression

- no eval;
- blocked properties;
- lexical scope;
- assignment;
- aliases;
- system variables.

## Events

- delegated event;
- target vs filter;
- filter before prevent/stop;
- debounce;
- throttle;
- once;
- outside;
- window/document;
- cleanup.

## Bindings

- text;
- show;
- reflected boolean;
- live property;
- attr;
- model;
- validation;
- one flush per transaction.

## Component

- init once;
- mount;
- dispose;
- nested scope;
- alias/ref;
- duplicate alias/ref;
- compatibility shorthand.

## Lists

- SSR non-empty adoption;
- SSR empty adoption;
- append;
- prepend;
- remove;
- reorder;
- stable node identity;
- duplicate key;
- nested list;
- cleanup;
- morph interaction.

## If

- initial true;
- initial false;
- toggle;
- nested component;
- nested list;
- cleanup;
- SSR adoption.

## Async

- resolved Promise;
- rejected Promise;
- error boundary;
- disposed scope ignores late write.

## Build

- generated artifact matches source;
- gzip size;
- runtime version;
- no forbidden eval/function generation.

---

# 31. Non-Goals

Không thêm trong đợt V2 này nếu không có use case bắt buộc:

```text
data-kit-switch
data-kit-case
data-kit-default
data-kit-hidden
data-kit-await
data-kit-teleport
data-kit-transition framework
global state manager mới
client router mới
CSS framework
large form framework
plugin system tùy ý
```

Không biến KitJS thành Alpine/Vue clone.

Không thêm syntax chỉ vì syntax đó quen thuộc ở framework khác.

Chỉ thêm khi nó tạo semantics mới hoặc giải quyết use case đã được chứng minh.

---

# 32. Definition of Done

Công việc chỉ được xem là hoàn thành khi:

- source-of-truth được sửa đúng nơi;
- generated artifacts được regenerate;
- current features không regression;
- legacy use cases quan trọng có fixtures;
- compatibility matrix có test;
- no eval/new Function được giữ;
- event pipeline deterministic;
- refs/aliases hoạt động;
- binding registry không cần full-document render cho mỗi mutation;
- block engine hoạt động cho list;
- `data-kit-item` là stable materialized identity;
- SSR empty/non-empty list hydrate đúng;
- `data-kit-if` dùng chung block engine;
- lifecycle dispose hoạt động;
- async error đi vào boundary;
- Go/client conformance vẫn pass;
- size report được tạo;
- docs phản ánh đúng code;
- Agent cung cấp migration notes.

---

# 33. Output Agent phải trả về

Sau khi hoàn thành, Agent phải cung cấp:

## Summary

- thay đổi kiến trúc chính;
- semantics đã chốt;
- compatibility được giữ.

## Changed Files

Danh sách file và vai trò của từng file.

## Tests

- test đã chạy;
- test pass/fail;
- benchmark hoặc size result;
- conformance result.

## Compatibility Notes

- API giữ nguyên;
- alias mới;
- deprecation;
- breaking change nếu có.

## Remaining Risks

- phần chưa hoàn thành;
- edge case chưa xử lý;
- technical debt;
- bước tiếp theo.

Không được chỉ nói “đã sửa xong” mà không nêu test và bằng chứng.

---

# 34. Nguyên tắc cuối cùng

```text
Không viết lại nếu có thể hội tụ.
Không copy legacy nếu có thể lấy semantics.
Không thêm syntax nếu không thêm semantics.
Không nhân đôi parser, scope model hoặc identity model.
Không sửa dist như source.
Không phá compatibility âm thầm.
Không hy sinh security để lấy DX.
Không gọi hoàn thành nếu chưa có test và hydration proof.
```

> **KitJS V2 phải giữ xương sống an toàn của kernel hiện tại, lấy lại đúng những năng lực đã được chứng minh trong legacy, và hội tụ chúng thành một runtime nhỏ, nhất quán, có lifecycle, có hydration contract và có thể phát triển lâu dài cùng Kitwork Engine.**
