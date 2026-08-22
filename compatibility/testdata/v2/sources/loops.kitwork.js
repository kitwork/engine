let total = 0;
for (let index = 0; index < 10; index++) {
  if (index == 4) {
    break;
  }
  total = total + index;
}
for (const number of [10, 20, 30]) {
  total = total + number;
  if (number == 20) {
    break;
  }
}
const result = total;

