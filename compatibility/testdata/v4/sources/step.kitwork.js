let text = "5";
const old = text++;
let missing = null;
missing--;
let flag = true;
++flag;
const result = [old, text, missing, flag, 7 % 0 == 7 % 0, 1 / 0 > 1e308];
