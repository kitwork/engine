// /users/:id — dynamic segment via the [id] folder.
import { route } from "kitwork"

export default route("/users/:id")
  .get((ctx) => {
    const user = ctx.db.where(u => u.id == ctx.params.id).first()
    if (!user) return ctx.notfound()
    return ctx.view({ user })
  })
