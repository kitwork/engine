// Tier ③ — capsule. LOGIC ONLY, shipped to the browser; no HTML here.
import { kit, db } from "kitwork/client"

// Hydrate the SSR-rendered list, then filter LIVE via a logic capsule.
kit.hydrate("users-list", (list) => {
  kit.on("[data-kit-filter]", "input", async (e) => {
    const q = e.target.value.trim().toLowerCase()

    // This looks like server code, but it is CAPTURED as a capsule, serialized to
    // bytecode and POSTed to /kitwork/run. The server runs it ONLY within this
    // identity's grant (e.g. { user: read }) and a gas budget — never with more.
    // `q` is passed as a bound input (closures are shipped explicitly, not magically).
    const users = await kit.run((q) =>
      db.table("user").where(u => u.name.lower().has(q)).list(20)
    , q)

    kit.render(list, users)   // Directive Sync — update in place, no reload
  })
})
