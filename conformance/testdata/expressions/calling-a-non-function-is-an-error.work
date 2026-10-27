const o = { x: 3, add: (n) => n + 1, pick: 7, a: 1 };
const dot = safe(() => o.x("k"));
const bracket = safe(() => o["x"]("k"));
const missing = safe(() => o.nope());
const called = "log";
const text = safe(() => called("sk-secret"));
let result = [dot.value, dot.error.code, dot.error.message, dot.error.at, bracket.error.message, bracket.error.at, missing.error.message, missing.error.at, text.error.message, text.error.at, o.add(1), o.pick("a").a].join("|");
