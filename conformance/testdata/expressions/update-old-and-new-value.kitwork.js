// x++ is the old x and ++x the new one, as in JavaScript (14/10/2026; both were the new x, and on a
// member the object). As a statement or a for's update nothing changed.
let x = 5;
const a = x++;
const b = ++x;
const c = x--;
const d = --x;
const o = { n: 1 };
const e = o.n++;
const f = ++o.n;
const list = [10];
const g = list[0]--;
let count = 0;
const next = () => count++;
const firsts = [next(), next()];
let i = 0;
const order = [i++, i++, ++i];
let result = [a, b, c, d, x, e, f, o.n, g, list, firsts, count, order];
