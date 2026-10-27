// A negative number is true, as in JavaScript: among numbers only 0 (-0 too) and NaN are false
// (14/10/2026; until then -1 || 5 was 5, and a filter dropped what fell below zero).
const position = "kitwork".indexOf("z");
const found = position ? "not first" : "first";
let branch = "no";
if (-2) {
    branch = "yes";
}
const kept = [3, 1, 2].filter((x) => x - 2);
const other = [5, 6].findIndex((x) => x - 9);
let result = [-1 || 5, !(-1), !!-0.5, -1 && "and", -0 || "zero", found, branch, kept, other];
