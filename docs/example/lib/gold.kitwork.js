// Shared logic — imported, never routed. Pure function: fetch + normalize.
import { http } from "kitwork"

export function fetchGold() {
  const res = http.get("https://edge-api.pnj.io/ecom-frontend/v1/get-gold-price?zone=11")
  if (res.status != 200) {
    return { ok: false, status: res.status }
  }
  const data = res.json().data.map(item => ({
    name: item.tensp,
    buy:  item.giamua,
    sell: item.giaban,
  }))
  return { ok: true, data }
}
