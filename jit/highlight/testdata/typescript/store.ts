// Kho dữ liệu có kiểu cho trang quán cà phê.
import type { Request, Response } from "./http";
import { log } from "./log";

export type Money = number & { readonly __brand: "money" };
export type Status = "open" | "closed" | "paused";
export type Line =
  | { kind: "drink"; id: string; size: Size }
  | { kind: "cake"; id: string; slices: 1 | 2 | 4 };

export enum Size { Small = "S", Medium = "M", Large = "L" }
export const enum Flag { None = 0, Hot = 1 << 0, Iced = 1 << 1 }

export interface Drink {
  id: string;
  name: string;           // tên món, ví dụ "Cà phê sữa đá"
  price: Money;
  sizes?: readonly Size[];
  tags: Array<string>;
  [extra: string]: unknown;
}

export interface Seasonal extends Drink, Partial<Record<"from" | "until", Date>> {
  readonly season: "spring" | "summer" | "autumn" | "winter";
}

type Handler<T extends object = {}> = (request: Request, context: T) => Promise<Response>;
type Pair = [name: string, price: number];
type Keys = keyof Drink;

const prices: Record<Size, number> = { [Size.Small]: 25_000, [Size.Medium]: 29_000, [Size.Large]: 35_000 };
const opening = { weekday: [7, 22], weekend: [8, 23] } as const;

export abstract class Repository<Item extends { id: string }> {
  protected readonly items = new Map<string, Item>();
  private static count: number = 0;
  #dirty = false;

  constructor(public readonly name: string, private limit = 100) {}

  abstract validate(item: Item): item is Item;

  get size(): number {
    return this.items.size;
  }

  upsert(item: Item): void {
    if (!this.validate(item)) throw new Error(`không hợp lệ: ${item.id}`);
    if (this.items.size >= this.limit) {
      log.warn(`${this.name} đầy (${this.limit})`);
      return;
    }
    this.items.set(item.id, item);
    this.#dirty = true;
    Repository.count++;
  }

  find<Key extends keyof Item>(key: Key, value: Item[Key]): Item | undefined {
    for (const item of this.items.values()) {
      if (item[key] === value) return item;
    }
    return undefined;
  }

  toJSON(): { name: string; items: Item[] } {
    return { name: this.name, items: [...this.items.values()] };
  }
}

export const menu = new (class extends Repository<Drink> {
  validate(item: Drink): item is Drink {
    return item.price > 0 && item.name.length > 0;
  }
})("menu");

export function priceOf(size: Size = Size.Medium): Money {
  return (prices[size] ?? 0) as Money;
}

export function isOpen(now: Date, status?: Status): boolean {
  const [start, end] = now.getDay() % 6 === 0 ? opening.weekend : opening.weekday;
  const hour = now.getHours();
  return status !== "closed" && hour >= start && hour < end;
}

export async function handle(request: Request): Promise<Response> {
  const body = (await request.json()) as { lines?: Line[] };
  const lines = body.lines ?? [];
  let sum = 0;
  for (const line of lines) {
    switch (line.kind) {
      case "drink":
        sum += priceOf(line.size);
        break;
      case "cake":
        sum += line.slices * 18_000;
        break;
      default:
        const never: never = line;
        return new Response(`món lạ: ${JSON.stringify(never)}`, { status: 400 });
    }
  }
  return new Response(JSON.stringify({ sum, ratio: sum / 1000 / 2 }), {
    headers: { "Content-Type": "application/json" },
  });
}

export namespace Report {
  export function daily(orders: ReadonlyArray<{ total: Money; at: Date }>): Map<string, number> {
    const days = new Map<string, number>();
    for (const order of orders) {
      const key = order.at.toISOString().slice(0, 10);
      days.set(key, (days.get(key) ?? 0) + order.total);
    }
    return days;
  }
}

const generic = <T,>(value: T): T[] => [value];
const pick = <T, K extends keyof T>(object: T, ...keys: K[]): Pick<T, K> =>
  Object.fromEntries(keys.map(key => [key, object[key]])) as Pick<T, K>;
declare global { interface Window { kitwork?: unknown } }
declare module "./log" { export const level: "debug" | "info"; }
export default { menu, priceOf, generic, pick, pattern: /\d+(?:\.\d+)?/g } satisfies object;
