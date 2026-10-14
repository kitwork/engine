// && and || run their right side only when the left does not decide (14/10/2026), and give the operand that decided.
let calls = 0;
const touch = (v) => { calls = calls + 1; return v; };
const skipped = [false && touch(1), "x" || touch(2), null && touch(3)];
const ran = [true && touch(4), 0 || touch(5)];
let branch = "none";
if (false && touch(6)) { branch = "and"; } else if (touch(0) || touch("or")) { branch = "or"; }
let result = [skipped, ran, branch, calls];
