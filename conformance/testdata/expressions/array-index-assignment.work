// a[i] = v sets item i of an array, as in JavaScript (14/10/2026): it appended v, whatever i was, so a
// count kept per bucket read 0 and a row replaced in place stayed as it was.
const a = [1, 2, 3];
a[0] = 9;
const counts = [0, 0, 0, 0];
for (const bid of [60, 55, 10]) {
    if (bid >= 50) {
        counts[0] = counts[0] + 1;
    } else {
        counts[3] += 1;
    }
}
const b = [1];
b[0]++;
const rows = [{ id: 1 }, { id: 2 }];
rows[0] = { id: 1, fresh: true };
const grown = [1];
grown[1] = 2;
let result = [a, counts, b, rows, grown];
