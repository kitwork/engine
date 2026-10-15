const makeAdder = (base) => (number) => base + number;
const addThirtySeven = makeAdder(37);
const result = addThirtySeven(5);

