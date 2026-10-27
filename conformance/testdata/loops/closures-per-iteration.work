// A closure made in a loop keeps the variables of its own iteration (14/10/2026), as in JavaScript.
const made = [];
for (const item of ["a", "b"]) {
    made.push(() => item);
}
for (let index = 0; index < 2; index++) {
    made.push(() => index);
}
const counters = [];
for (const name of ["p", "q"]) {
    let count = 0;
    counters.push(() => { count = count + 1; return name + count; });
}
counters[0]();
let result = [made.map((read) => read()), counters[0](), counters[1]()];
