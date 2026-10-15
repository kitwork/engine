// continue goes on with the next iteration, and the counting for runs its update first, as in
// JavaScript (14/10/2026; it was a compile error). In a switch it is the loop's, and a closure made in
// the loop keeps its iteration.
const odd = [];
for (const x of [1, 2, 3, 4, 5]) {
    if (x % 2 == 0) {
        continue;
    }
    odd.push(x);
}
const counted = [];
for (let i = 0; i < 6; i++) {
    if (i == 1) continue;
    switch (i) {
    case 3:
        continue;
    default:
        counted.push(i);
    }
}
const read = [];
for (let i = 0; i < 4; i++) {
    if (i == 2) {
        continue;
    }
    read.push(() => i);
}
let result = [odd, counted, read.map((f) => f())];
