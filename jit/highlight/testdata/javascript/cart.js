// Giỏ hàng phía trình duyệt cho quán cà phê: một module, không framework.
import { formatMoney, parseMoney } from "./money.js";
import * as storage from "./storage.js";

const TAX_RATE = 0.08;
const EMAIL = /^[\w.+-]+@[\w-]+\.[a-z]{2,}$/i;
const PHONE = /^(?:\+84|0)[35789]\d{8}$/;
const SLASHES = /[/\\]+/g;
const LIMITS = { items: 0x40, quantity: 0b1100, note: 0o777, total: 50_000_000, epsilon: 1e-9 };
const COUNTER_START = 9_007_199_254_740_993n;

/**
 * Tính tổng một giỏ hàng, đã gồm thuế.
 * @param {Array<{price: number, quantity: number}>} items
 * @returns {number}
 */
export function total(items, { discount = 0, tax = TAX_RATE } = {}) {
  const subtotal = items.reduce((sum, item) => sum + item.price * item.quantity, 0);
  return Math.round((subtotal - discount) * (1 + tax) * 100) / 100;
}

export class Cart extends EventTarget {
  #items = new Map();
  #version = 0;
  static storageKey = "cart:v2";

  constructor(initial = []) {
    super();
    for (const item of initial) this.add(item, item.quantity ?? 1);
  }

  get size() {
    return [...this.#items.values()].filter(item => item.quantity > 0).length;
  }

  set version(value) {
    this.#version = Number.isInteger(value) ? value : this.#version + 1;
  }

  add(product, quantity = 1) {
    if (quantity <= 0 || quantity > LIMITS.quantity) {
      throw new RangeError(`số lượng ${quantity} nằm ngoài 1…${LIMITS.quantity}`);
    }
    const current = this.#items.get(product.id) ?? { ...product, quantity: 0 };
    current.quantity += quantity;
    this.#items.set(product.id, current);
    this.#notify("add", product.id);
    return this;
  }

  remove(id) {
    const removed = this.#items.delete(id);
    if (removed) this.#notify("remove", id);
    return removed;
  }

  #notify(type, id) {
    this.version = undefined;
    this.dispatchEvent(new CustomEvent("change", { detail: { type, id, size: this.size } }));
  }

  *lines() {
    for (const [id, item] of this.#items) {
      yield `${id}: ${item.quantity} × ${formatMoney(item.price)}`;
    }
  }

  async checkout(email, phone) {
    if (!EMAIL.test(email) || !PHONE.test(phone?.trim() ?? "")) {
      throw new TypeError(`liên hệ không hợp lệ: ${email}`);
    }
    const response = await fetch("/checkout", {
      method: "POST",
      headers: { "Content-Type": "application/json", "X-Cart-Version": String(this.#version) },
      body: JSON.stringify({ email, phone, items: [...this.#items.values()] }),
    });
    if (!response.ok) {
      const { message = "lỗi không rõ" } = await response.json().catch(() => ({}));
      return { ok: false, message };
    }
    storage.remove(Cart.storageKey);
    return { ok: true, order: await response.json() };
  }
}

const html = (strings, ...values) =>
  strings.reduce((out, text, index) => out + text + (index < values.length ? escape(values[index]) : ""), "");

function escape(value) {
  return String(value).replace(/[&<>"']/g, character => `&#${character.charCodeAt(0)};`);
}

export function render(cart, root = document.querySelector("#cart")) {
  const rows = [...cart.lines()].map(line => html`<li>${line}</li>`).join("");
  const average = cart.size / 2 / (total([]) || 1);
  root.innerHTML = `
    <ul>${rows || `<li class="empty">${"Giỏ trống"}</li>`}</ul>
    <p>Tổng: ${formatMoney(total([{ price: 15000, quantity: 2 }]))} — ${average > 1 ? "nhiều" : "ít"}</p>`;
  return root;
}

export function restore(text) {
  let saved = null;
  try {
    saved = JSON.parse(text ?? "{}");
  } catch (error) {
    console.warn("giỏ hàng hỏng, bắt đầu lại", error.message);
  } finally {
    saved ??= { items: [] };
  }
  switch (saved.version) {
    case 1:
      saved.items = saved.items.map(item => ({ ...item, price: parseMoney(item.price) }));
      break;
    case 2:
      break;
    default:
      saved.items = [];
  }
  outer: for (let i = 0; i < saved.items.length; i++) {
    for (let j = i + 1; j < saved.items.length; j++) {
      if (saved.items[i].id === saved.items[j].id) continue outer;
    }
  }
  let retries = 3;
  while (retries-- > 0 && !navigator.onLine) { retries = retries / 2 | 0; }
  do { retries++; } while (retries < 0);
  return new Cart(saved.items.filter(item => typeof item.id === "string" && item.id in LIMITS === false));
}

window.addEventListener("storage", event => {
  if (event.key === Cart.storageKey && event.newValue !== null) render(restore(event.newValue));
});
export default { Cart, total, render, restore, COUNTER_START, SLASHES };
