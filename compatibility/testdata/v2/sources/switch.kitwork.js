const stage = "review";
let result = "";
switch (stage) {
case "draft":
  result = result + "draft:";
case "review":
  result = result + "review:";
case "ready":
  result = result + "ready";
  break;
default:
  result = "unknown";
}

