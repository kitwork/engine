// const and let live in their block (14/10/2026): an inner one never changes the outer one, a nested
// function's declaration stays in it, an assigned parameter stays in its function, and a block's
// variable is not seen after the block.
let x = 1;
let seen = 0;
if (x > 0) {
    const x = 2;
    seen = x;
}
let error = "top";
const save = () => {
    const { error } = { error: "inner" };
    return error;
};
const bump = (x) => {
    x = x + 10;
    return x;
};
if (true) {
    const hidden = "block";
}
let result = [x, seen, save(), error, bump(5), x, hidden];
