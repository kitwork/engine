// /api/gold — no page.kitwork.html in this folder ⇒ it's a JSON API.
import { route } from "kitwork"
import { fetchGold } from "../../../lib/gold.kitwork.js"

export default route("/api/gold")
  .cache("1h")
  .get((ctx) => {
    const gold = fetchGold()
    if (!gold.ok) {
      return ctx.status(500).json({ error: "Failed to fetch gold price", status: gold.status })
    }
    return ctx.json({ success: true, count: gold.data.length, data: gold.data })
  })
