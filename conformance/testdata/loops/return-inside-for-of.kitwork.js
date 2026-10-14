// A return inside for…of returns (14/10/2026): the verifier refused the program, since the loop's
// iterable and index stayed on the stack.
const first = (list) => {
    for (const item of list) {
        if (item > 1) {
            return item;
        }
    }
    return null;
};
let result = [first([1, 2, 3]), first([1])];
