#!/usr/bin/env python3
# Vendor script for jit/fonts: fetch Google Fonts CSS (with a browser UA so we get woff2), download
# the already-subset woff2 (incl. the Vietnamese range), and generate catalog_gen.go. The fonts are
# OFL/Apache, so self-hosting them is allowed (see FONTS_LICENSE). Run from the repo root:
#     python engine/jit/fonts/vendor.py
import urllib.request, re, os

UA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
ALLOWED = {"latin", "latin-ext", "vietnamese"}  # keep these subsets; drop cyrillic/greek/etc.

FAMILIES = {
    "outfit": {
        "name": "Outfit",
        "css": "https://fonts.googleapis.com/css2?family=Outfit:wght@300;400;500;700;900&display=swap",
        "fallback": "ui-sans-serif, system-ui, -apple-system, sans-serif",
    },
    "fira-code": {
        "name": "Fira Code",
        "css": "https://fonts.googleapis.com/css2?family=Fira+Code:wght@400;500&display=swap",
        "fallback": "ui-monospace, SFMono-Regular, Menlo, monospace",
    },
}

HERE = os.path.dirname(os.path.abspath(__file__))
BASE = os.path.join(HERE, "families")
block_re = re.compile(r"/\*\s*([\w-]+)\s*\*/\s*@font-face\s*\{(.*?)\}", re.S)


def fetch(url):
    return urllib.request.urlopen(urllib.request.Request(url, headers={"User-Agent": UA}), timeout=40).read()


def field(block, name):
    m = re.search(name + r"\s*:\s*([^;]+);", block)
    return m.group(1).strip() if m else ""


def goquote(s):
    return '"' + s.replace("\\", "\\\\").replace('"', '\\"') + '"'


def main():
    entries = {}
    for slug, info in FAMILIES.items():
        css = fetch(info["css"]).decode("utf-8")
        faces = []
        for subset, block in block_re.findall(css):
            if subset not in ALLOWED:
                continue
            weight = field(block, "font-weight")
            style = field(block, "font-style") or "normal"
            um = re.search(r"url\(([^)]+)\)", block)
            if not um:
                continue
            woff_url = um.group(1).strip().strip('"').strip("'")
            unicode_range = field(block, "unicode-range")
            fname = "%s-%s.woff2" % (weight, subset)
            if style != "normal":
                fname = "%s-%s-%s.woff2" % (weight, subset, style)
            outdir = os.path.join(BASE, slug)
            os.makedirs(outdir, exist_ok=True)
            with open(os.path.join(outdir, fname), "wb") as fh:
                fh.write(fetch(woff_url))
            faces.append({"weight": weight, "style": style, "subset": subset,
                          "unicode": unicode_range, "file": "families/%s/%s" % (slug, fname)})
        entries[slug] = faces
        print(slug, len(faces), "faces:", [f["subset"] + "/" + f["weight"] for f in faces])

    out = ["// Code generated from Google Fonts CSS (fonts are OFL/Apache) — DO NOT EDIT.",
           "// Regenerate with: python engine/jit/fonts/vendor.py", "", "package fonts", "",
           "var catalog = map[string]family{"]
    for slug, info in FAMILIES.items():
        out.append("\t%s: {" % goquote(slug))
        out.append("\t\tname:     %s," % goquote(info["name"]))
        out.append("\t\tfallback: %s," % goquote(info["fallback"]))
        out.append("\t\tfaces: []face{")
        for f in entries[slug]:
            out.append("\t\t\t{weight: %s, style: %s, subset: %s, file: %s, unicode: %s}," % (
                f["weight"], goquote(f["style"]), goquote(f["subset"]), goquote(f["file"]), goquote(f["unicode"])))
        out.append("\t\t},")
        out.append("\t},")
    out.append("}")
    with open(os.path.join(HERE, "catalog_gen.go"), "w", encoding="utf-8") as fh:
        fh.write("\n".join(out) + "\n")

    total = sum(os.path.getsize(os.path.join(dp, f)) for dp, _, fs in os.walk(BASE) for f in fs)
    print("catalog_gen.go written | total woff2:", round(total / 1024), "KB")


if __name__ == "__main__":
    main()
