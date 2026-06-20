// ── Composition root ─────────────────────────────────────────────
// AFTER: this file is a MAP, not a place for logic.
// One glance here = the whole site. (Run `kitwork routes` for the full tree.)

import { router, database } from "kitwork"

// route modules (each declares its own path)
import usersList   from "./app/users/index.kitwork.js"
import usersDetail from "./app/users/[id]/index.kitwork.js"
import goldApi     from "./app/api/gold/index.kitwork.js"

// per-tenant singleton
const db = database.connect("system")
router.context({ db })          // DI-lite: inject ctx.db into every module

// explicit map (default — no magic, full control)
router.use(usersList)
router.use(usersDetail)
router.use(goldApi)

// Pages with no logic (about, founder, story, docs, …) auto-mount from app/.
// Only routes that need data/guards need a module + a line here.
