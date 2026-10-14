// Declaring a name twice in one block is a compile error (14/10/2026), as in JavaScript.
const { value, error } = { value: 1, error: null };
const { value: second, error } = { value: 2, error: null };
