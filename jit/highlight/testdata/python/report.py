#!/usr/bin/env python3
"""Báo cáo doanh thu: đọc CSV, in tổng theo món.

Một docstring nhiều dòng với "dấu nháy" bên trong, và cả '''ba nháy đơn'''.
"""
from __future__ import annotations

import asyncio
import csv
import functools
import sys
from dataclasses import dataclass, field
from decimal import Decimal
from enum import Enum
from typing import Iterable, Optional

TAX_RATE = Decimal("0.08")
LIMIT = 1_000_000
MASK = 0xFF | 0b1010 | 0o755
COMPLEX = 3 + 4j
HALF = 1. / 2
SMALL = 2.5e-3
NOTHING = None

type Totals = dict[str, Decimal]


class Size(Enum):
    SMALL = "S"
    LARGE = "L"


@dataclass(frozen=True)
class Line:
    name: str
    price: Decimal
    quantity: int = 1
    tags: list[str] = field(default_factory=list)

    @property
    def total(self) -> Decimal:
        return self.price * self.quantity

    def describe(self, width: int = 20) -> str:
        return f"{self.name!r:<{width}} × {self.quantity:>3d} = {self.total:,.0f} ₫"


class ReportError(ValueError):
    """Raised when a CSV row cannot be read."""


@functools.lru_cache(maxsize=None)
def size_of(code: str) -> Size:
    match code.strip().upper():
        case "S" | "SMALL":
            return Size.SMALL
        case "L" | "LARGE":
            return Size.LARGE
        case _:
            raise ReportError(f"cỡ lạ: {code}")


def read_lines(path: str, *, encoding: str = "utf-8") -> Iterable[Line]:
    with open(path, newline="", encoding=encoding) as handle:
        for row in csv.DictReader(handle):
            if not (name := row.get("name")):
                continue
            yield Line(name, Decimal(row['price']), int(row.get("quantity", 1)))


def summarize(lines: Iterable[Line], minimum: Optional[Decimal] = None) -> Totals:
    totals: Totals = {}
    for line in lines:
        totals[line.name] = totals.get(line.name, Decimal(0)) + line.total
    return {name: value for name, value in totals.items() if minimum is None or value >= minimum}


def render(totals: Totals) -> str:
    width = max((len(name) for name in totals), default=0)
    rows = [f"{name:<{width}}  {value * (1 + TAX_RATE):>12,.0f} ₫" for name, value in totals.items()]
    header = f"{'Món':<{width}}  {'Tổng':>12}"
    rule = "─" * (width + 16)
    return "\n".join([header, rule, *rows])


counter = 0


def bump() -> int:
    global counter
    counter += 1
    return counter


def make_adder(step):
    total = 0

    def add(amount=step):
        nonlocal total
        total += amount
        return total

    return add


async def fetch(source: str) -> bytes:
    await asyncio.sleep(0)
    return b"name,price\n" + source.encode()


async def main(argv: list[str]) -> int:
    try:
        totals = summarize(read_lines(argv[1]))
    except (IndexError, FileNotFoundError) as error:
        print(f"lỗi: {error!r} — cần đường dẫn {{csv}}", file=sys.stderr)
        return 2
    finally:
        pass
    print(render(totals))
    for name, value in sorted(totals.items(), key=lambda item: -item[1]):
        assert value >= 0, f"âm: {name=}"
    raw = r"C:\data\no\escapes" + b"bytes".decode() + rb'\x00'.hex()
    dash = "\N{EM DASH}" + '\u00e9' + "tab\tand \"quotes\""
    matrix = [[1, 2], [3, 4]]
    del matrix[0]
    data = await fetch("cà phê,25000")
    print(raw, dash, data, ..., size_of("s") is Size.SMALL, sep="\n")
    return 0 if totals else 1


if __name__ == "__main__":
    sys.exit(asyncio.run(main(sys.argv)))
