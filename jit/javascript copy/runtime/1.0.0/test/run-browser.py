#!/usr/bin/env python3
"""Run the Kitwork runtime browser conformance pages without a web server."""

from __future__ import annotations

import json
import os
import shutil
import sys
from pathlib import Path

from playwright.sync_api import sync_playwright

ROOT = Path(__file__).resolve().parents[1]
BUNDLE = ROOT / "dist" / "kitwork-runtime.js"
TESTS = [
    ROOT / "test" / "runtime.test.html",
    ROOT / "test" / "services.test.html",
    ROOT / "test" / "lifecycle.test.html",
    ROOT / "test" / "drive.test.html",
    ROOT / "test" / "drive-html-root.test.html",
]


def inline_bundle(html: str, bundle: str) -> str:
    marker = '<script src="../dist/kitwork-runtime.js"></script>'
    if marker not in html:
        raise RuntimeError(f"Bundle marker is missing from test page")
    safe_bundle = bundle.replace("</script>", "<\\/script>")
    return html.replace(marker, f"<script>\n{safe_bundle}\n</script>")



def run_page(browser, path: Path, bundle: str) -> dict:
    page = browser.new_page()
    page_errors: list[str] = []
    console_errors: list[str] = []
    page.on("pageerror", lambda error: page_errors.append(str(error)))
    page.on(
        "console",
        lambda message: console_errors.append(message.text)
        if message.type == "error"
        else None,
    )

    page.set_content(
        inline_bundle(path.read_text(encoding="utf-8"), bundle),
        wait_until="domcontentloaded",
        timeout=30_000,
    )

    complete = False
    for _ in range(100):
        page.wait_for_timeout(100)
        if page.locator("html").get_attribute("data-test-complete") == "true":
            complete = True
            break

    results = page.locator("#results")
    raw = results.text_content() if results.count() else ""
    total = int(results.get_attribute("data-total") or 0) if results.count() else 0
    passed = int(results.get_attribute("data-passed") or 0) if results.count() else 0
    failed = int(results.get_attribute("data-failed") or (0 if complete else 1)) if results.count() else 1

    parsed = None
    try:
        parsed = json.loads(raw or "{}")
    except json.JSONDecodeError:
        parsed = {"raw": raw}

    report = {
        "name": path.name,
        "complete": complete,
        "total": total,
        "passed": passed,
        "failed": failed,
        "page_errors": page_errors,
        "console_errors": console_errors,
        "result": parsed,
    }
    page.close()
    return report



def main() -> int:
    bundle = BUNDLE.read_text(encoding="utf-8")
    reports: list[dict] = []

    with sync_playwright() as playwright:
        chromium_path = (
            os.environ.get("CHROMIUM_PATH")
            or shutil.which("chromium")
            or shutil.which("chromium-browser")
            or shutil.which("google-chrome")
        )
        launch_options = {
            "headless": True,
            "args": ["--no-sandbox", "--disable-dev-shm-usage", "--disable-gpu"],
        }
        if chromium_path:
            launch_options["executable_path"] = chromium_path
        browser = playwright.chromium.launch(**launch_options)
        try:
            for test in TESTS:
                reports.append(run_page(browser, test, bundle))
        finally:
            browser.close()

    summary = {
        "schema": "kitwork-runtime-browser-report/1",
        "runtime": "1.0.0-draft",
        "tests": reports,
        "total": sum(report["total"] for report in reports),
        "passed": sum(report["passed"] for report in reports),
        "failed": sum(report["failed"] for report in reports),
        "page_errors": sum(len(report["page_errors"]) for report in reports),
        "console_errors": sum(len(report["console_errors"]) for report in reports),
    }
    (ROOT / "test" / "browser-report.json").write_text(
        json.dumps(summary, indent=2, ensure_ascii=False) + "\n",
        encoding="utf-8",
    )
    print(json.dumps(summary, indent=2, ensure_ascii=False))

    broken = (
        summary["failed"]
        or summary["page_errors"]
        or any(not report["complete"] for report in reports)
    )
    return 1 if broken else 0


if __name__ == "__main__":
    sys.exit(main())
