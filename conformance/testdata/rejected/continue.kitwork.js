// continue belongs to a loop (14/10/2026): in a switch with no loop around it, it is JavaScript's
// SyntaxError. In a loop it goes on with the next iteration (loops/continue.kitwork.js).
switch (1) {
case 1:
    continue;
}
