// route module — imports router (kitwork) + a helper from another file (lib),
// then registers GET /hello. Importing this file registers the route.
import router from "kitwork/router";
import { greet } from "../lib/greet.kitwork.js";

router.get("/hello").handle((response) => {
  return response.text(greet("world"));
});
