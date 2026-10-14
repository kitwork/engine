// ?? keeps 0, "" and false; ?. skips the rest of its chain, arguments included, when its left is null (14/10/2026).
let calls = 0;
const touch = (v) => { calls = calls + 1; return v; };
const none = null;
const o = { a: { b: 0, list: [1, 2] }, twice: (n) => n * 2 };
const nullish = [none ?? "d", 0 ?? "d", "" ?? "d", false ?? "d", o.missing ?? "d"];
const chained = [none?.a.b, none?.m(touch(1)), o?.a?.b, o.a.list?.[1], o.twice?.(3), o.gone?.(touch(2)), none?.[0]];
let result = [nullish, chained, calls];
