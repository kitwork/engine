#!/usr/bin/env python3
# Vendor script for jit/fonts: fetch Google Fonts CSS (with a browser UA so we get woff2), download
# the already-subset woff2 (incl. the Vietnamese range), and generate catalog_gen.go. The fonts are
# OFL/Apache, so self-hosting them is allowed (see FONTS_LICENSE). Run from the repo root:
#     python engine/jit/fonts/vendor.py                                  # every family
#     python engine/jit/fonts/vendor.py google-sans-flex jetbrains-mono  # only these
# Naming families re-downloads only those; every other family keeps its woff2 and its faces exactly
# as catalog_gen.go has them (read back from the file this script wrote), so adding a family never
# changes the bytes a page already loads.
#
# A variable family asked for as a range (wght@1..1000) comes back as ONE file per subset for every
# weight: font-weight "1 1000" is written as the face {weight: 1, through: 1000} and the file
# families/<family>/1-1000-<subset>.woff2.
import urllib.request, re, os, sys

UA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
ALLOWED = {"latin", "latin-ext", "vietnamese"}  # keep these subsets; drop cyrillic/greek/etc.

FAMILIES = {
    "outfit": {
        "name": "Outfit",
        "css": "https://fonts.googleapis.com/css2?family=Outfit:wght@300;400;500;700;900&display=swap",
        "fallback": "ui-sans-serif, system-ui, -apple-system, sans-serif",
        # the weight axis of its variable font: Google serves one file per subset for every weight
        "weights": (100, 900),
        # the template path (Render) and Lookup know it by name: one of the two families vendored
        # before router.fonts could name any Google font
        "template": True,
    },
    "fira-code": {
        "name": "Fira Code",
        "css": "https://fonts.googleapis.com/css2?family=Fira+Code:wght@400;500&display=swap",
        "fallback": "ui-monospace, SFMono-Regular, Menlo, monospace",
        "weights": (300, 700),
        "template": True,
    },
    # The engine's default typefaces (fonts.DefaultBody, fonts.DefaultCode), embedded so the default
    # works offline, and JetBrains Mono, the code face before Google Sans Code, kept for a site that
    # names it. Reached through Find / FaceCSS by name, not through Render or Lookup.
    "google-sans-flex": {
        "name": "Google Sans Flex",
        "css": "https://fonts.googleapis.com/css2?family=Google+Sans+Flex:opsz,wght@6..144,1..1000&display=swap",
        "fallback": "ui-sans-serif, system-ui, -apple-system, sans-serif",
        "weights": (1, 1000),
    },
    # MONO is left out of the request, so Google serves it at its default, 1: every letter one width.
    "google-sans-code": {
        "name": "Google Sans Code",
        "css": "https://fonts.googleapis.com/css2?family=Google+Sans+Code:wght@300..800&display=swap",
        "fallback": "ui-monospace, SFMono-Regular, Menlo, monospace",
        "weights": (300, 800),
    },
    "jetbrains-mono": {
        "name": "JetBrains Mono",
        "css": "https://fonts.googleapis.com/css2?family=JetBrains+Mono:wght@100..800&display=swap",
        "fallback": "ui-monospace, SFMono-Regular, Menlo, monospace",
        "weights": (100, 800),
    },
}

HERE = os.path.dirname(os.path.abspath(__file__))
BASE = os.path.join(HERE, "families")
CATALOG = os.path.join(HERE, "catalog_gen.go")
block_re = re.compile(r"/\*\s*([\w-]+)\s*\*/\s*@font-face\s*\{(.*?)\}", re.S)
face_line_re = re.compile(
    r'^\t\t\t\{weight: (\d+), (?:through: (\d+), )?style: "([^"]*)", subset: "([^"]*)", '
    r'file: "(families/([a-z0-9-]+)/[^"]*)", unicode: "([^"]*)"\},$')


def fetch(url):
    return urllib.request.urlopen(urllib.request.Request(url, headers={"User-Agent": UA}), timeout=40).read()


def field(block, name):
    m = re.search(name + r"\s*:\s*([^;]+);", block)
    return m.group(1).strip() if m else ""


def goquote(s):
    return '"' + s.replace("\\", "\\\\").replace('"', '\\"') + '"'


def kept_faces():
    """The faces catalog_gen.go lists today, by family — what a family not named on the command line keeps."""
    faces = {}
    if not os.path.exists(CATALOG):
        return faces
    with open(CATALOG, encoding="utf-8") as fh:
        for line in fh.read().splitlines():
            m = face_line_re.match(line)
            if not m:
                continue
            weight, through, style, subset, path, slug, unicode_range = m.groups()
            faces.setdefault(slug, []).append({"weight": weight, "through": through, "style": style,
                                               "subset": subset, "file": path, "unicode": unicode_range})
    return faces


def vendor(slug, info):
    css = fetch(info["css"]).decode("utf-8")
    faces = []
    for subset, block in block_re.findall(css):
        if subset not in ALLOWED:
            continue
        weight = field(block, "font-weight").split()
        through = None
        if len(weight) == 2:
            weight, through = weight
        else:
            weight = weight[0]
        style = field(block, "font-style") or "normal"
        um = re.search(r"url\(([^)]+)\)", block)
        if not um:
            continue
        woff_url = um.group(1).strip().strip('"').strip("'")
        unicode_range = field(block, "unicode-range")
        name = weight if through is None else "%s-%s" % (weight, through)
        fname = "%s-%s.woff2" % (name, subset)
        if style != "normal":
            fname = "%s-%s-%s.woff2" % (name, subset, style)
        outdir = os.path.join(BASE, slug)
        os.makedirs(outdir, exist_ok=True)
        body = fetch(woff_url)
        if body[:4] != b"wOF2":
            sys.exit("%s: %s is not a woff2" % (slug, woff_url))
        with open(os.path.join(outdir, fname), "wb") as fh:
            fh.write(body)
        faces.append({"weight": weight, "through": through, "style": style, "subset": subset,
                      "unicode": unicode_range, "file": "families/%s/%s" % (slug, fname)})
    return faces


def main():
    only = sys.argv[1:]
    for slug in only:
        if slug not in FAMILIES:
            sys.exit("unknown family %r (known: %s)" % (slug, ", ".join(FAMILIES)))
    kept = kept_faces()
    entries = {}
    for slug, info in FAMILIES.items():
        if only and slug not in only:
            if slug not in kept:
                sys.exit("%s is not in catalog_gen.go yet: name it to vendor it" % slug)
            entries[slug] = kept[slug]
            print(slug, "kept:", len(kept[slug]), "faces")
            continue
        entries[slug] = vendor(slug, info)
        print(slug, len(entries[slug]), "faces:", [f["subset"] + "/" + f["weight"] + ("-" + f["through"] if f["through"] else "")
                                                  for f in entries[slug]])

    out = ["// Code generated from Google Fonts CSS (fonts are OFL/Apache) — DO NOT EDIT.",
           "// Regenerate with: python engine/jit/fonts/vendor.py", "", "package fonts", "",
           "var catalog = map[string]family{"]
    for slug, info in FAMILIES.items():
        out.append("\t%s: {" % goquote(slug))
        out.append("\t\tname:     %s," % goquote(info["name"]))
        out.append("\t\tfallback: %s," % goquote(info["fallback"]))
        if "weights" in info:
            out.append("\t\tweights:  [2]int{%d, %d}," % info["weights"])
        if info.get("template"):
            out.append("\t\ttemplate: true,")
        out.append("\t\tfaces: []face{")
        for f in entries[slug]:
            through = "through: %s, " % f["through"] if f["through"] else ""
            out.append("\t\t\t{weight: %s, %sstyle: %s, subset: %s, file: %s, unicode: %s}," % (
                f["weight"], through, goquote(f["style"]), goquote(f["subset"]), goquote(f["file"]), goquote(f["unicode"])))
        out.append("\t\t},")
        out.append("\t},")
    out.append("}")
    # keep the line endings the checkout has (CRLF on a Windows checkout, LF elsewhere)
    newline = "\n"
    if os.path.exists(CATALOG):
        with open(CATALOG, "rb") as fh:
            if b"\r\n" in fh.read():
                newline = "\r\n"
    with open(CATALOG, "w", encoding="utf-8", newline=newline) as fh:
        fh.write("\n".join(out) + "\n")

    for slug in FAMILIES:
        folder = os.path.join(BASE, slug)
        size = sum(os.path.getsize(os.path.join(folder, f)) for f in os.listdir(folder)) if os.path.isdir(folder) else 0
        print("%-18s %7d bytes" % (slug, size))
    total = sum(os.path.getsize(os.path.join(dp, f)) for dp, _, fs in os.walk(BASE) for f in fs)
    print("catalog_gen.go written | total woff2:", round(total / 1024), "KB")


if __name__ == "__main__":
    main()
