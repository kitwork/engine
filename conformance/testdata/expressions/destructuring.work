// Renaming, reserved-word and string keys, defaults (missing or null) and nested patterns (14/10/2026).
const { value: user, error: problem = "none", default: fallback, "content-type": type } = { value: "ann", error: null, default: 1, "content-type": "html" };
const { a: { b, c: [first, second = 9] }, d: { e } = { e: 7 } } = { a: { b: 2, c: [5] }, d: null };
let x = 0;
let y = 0;
;[x, y = 3] = [1];
let result = [user, problem, fallback, type, b, first, second, e, x, y];
