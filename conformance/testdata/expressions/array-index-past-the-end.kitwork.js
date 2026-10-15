// a[i] = v past the end pads the gap with null, as JavaScript's holes read undefined (14/10/2026; it
// appended v whatever i was). A key that is no index fails: an array of Kitwork has no properties.
const a = [1, 2];
a[4] = 5;
a[a.length] = 6;
const counts = [];
counts[3] = 1;
const b = [1, 2];
const negative = safe(() => { b[-1] = 0; });
const named = safe(() => { b.total = 0; });
let result = [a, a.length, counts, negative.error.code, named.error.code, b];
