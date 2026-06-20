// Tier ① — SSR, trusted. Server handler renders the sibling page.kitwork.html.
import { route } from "kitwork"

export default route("/users")
  .get((ctx) => ctx.view({ users: ctx.db.table("user").list(5) }))
