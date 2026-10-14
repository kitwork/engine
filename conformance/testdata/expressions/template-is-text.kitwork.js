// A template is text (14/10/2026): `${a}${b}` of two numbers joins them, `${n}` alone is a string, and
// a brace inside a string of ${…} is text.
const a = 1;
const b = 2;
let result = [`${a}${b}`, `${5}`.length, `a${"}"}b`];
