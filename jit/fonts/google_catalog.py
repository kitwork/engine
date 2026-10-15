#!/usr/bin/env python3
# Generator for jit/fonts' Google Fonts catalog: reads Google's family list (the metadata the
# fonts.google.com site itself loads) and writes google_catalog.json, the trimmed copy the engine
# embeds so router.fonts can name ANY Google font and the compiler can check the name, the language
# and the weights without the network. Run from the repo root:
#
#     python engine/jit/fonts/google_catalog.py                      # fetch the list from Google
#     python engine/jit/fonts/google_catalog.py --from metadata.json # a copy saved earlier
#
# Per family it keeps: the name, the category, the subsets (without "menu", which is the few glyphs
# Google uses to draw the name in its own menu), the static styles (weights, and the weights that
# have an italic), the variable axes (tag, min, max), and whether the family is open source (only
# those are downloaded). One family per line, so a refresh reads as a diff.
#
#     {"categories":[...],
#     "subsets":[...],
#     "families":[
#     ["Google Sans Flex",3,[2,5,6,...],[1,100,...,1000],[],[["GRAD",0,100],...,["wght",1,1000]]],
#     ...
#     ]}
#
# A family is [name, category index, subset indexes, normal weights, italic weights, axes, open
# source]; the trailing parts are left out when empty (open source: when 1).
import json, os, re, sys, urllib.request

SOURCE = "https://fonts.google.com/metadata/fonts"
HERE = os.path.dirname(os.path.abspath(__file__))
OUT = os.path.join(HERE, "google_catalog.json")
UA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36"

# The same rules the engine applies when it reads the file (google.go): a name is letters, digits and
# single spaces; its slug (lowercase, hyphens) is what reaches the cache folder on disk.
NAME = re.compile(r"^[A-Za-z0-9]+( [A-Za-z0-9]+)*$")
SUBSET = re.compile(r"^[a-z0-9]+(-[a-z0-9]+)*$")
TAG = re.compile(r"^[A-Za-z0-9]{4}$")


def load(argv):
    if len(argv) >= 3 and argv[1] == "--from":
        with open(argv[2], encoding="utf-8") as fh:
            return fh.read()
    if len(argv) > 1:
        sys.exit("usage: google_catalog.py [--from metadata.json]")
    request = urllib.request.Request(SOURCE, headers={"User-Agent": UA})
    with urllib.request.urlopen(request, timeout=60) as response:
        return response.read().decode("utf-8")


def number(v):
    v = float(v)
    return int(v) if v.is_integer() else v


def main():
    raw = load(sys.argv)
    # the body starts with a )]}' guard line before the JSON
    data = json.loads(raw[raw.index("{"):])
    families = sorted(data["familyMetadataList"], key=lambda f: f["family"])

    skipped, keys = [], {}
    kept = []
    for f in families:
        name = f["family"]
        if not NAME.match(name):
            skipped.append(name)
            continue
        key = name.replace(" ", "").lower()
        if key in keys:
            sys.exit("two families read the same once case and spaces are ignored: %r and %r" % (keys[key], name))
        keys[key] = name
        kept.append(f)

    categories = sorted({f["category"] for f in kept})
    subsets = sorted({s for f in kept for s in f["subsets"] if s != "menu"})
    for s in subsets:
        if not SUBSET.match(s):
            sys.exit("unexpected subset name %r" % s)

    rows = []
    for f in kept:
        normal, italic = [], []
        for style in f["fonts"]:
            m = re.match(r"^(\d+)(i?)$", style)
            if not m or not 1 <= int(m.group(1)) <= 1000:
                sys.exit("%s: unexpected style %r" % (f["family"], style))
            (italic if m.group(2) else normal).append(int(m.group(1)))
        axes = []
        for a in f["axes"]:
            if not TAG.match(a["tag"]):
                sys.exit("%s: unexpected axis %r" % (f["family"], a["tag"]))
            axes.append([a["tag"], number(a["min"]), number(a["max"])])
        row = [f["family"], categories.index(f["category"]),
               [subsets.index(s) for s in f["subsets"] if s != "menu"],
               sorted(normal), sorted(italic), axes, 1 if f["isOpenSource"] else 0]
        # leave out the trailing parts that say nothing: open source (1), no axes, no italics
        if row[6] == 1:
            row.pop()
            if not row[5]:
                row.pop()
                if not row[4]:
                    row.pop()
        rows.append(row)

    compact = lambda v: json.dumps(v, separators=(",", ":"), ensure_ascii=True)
    text = ('{"categories":' + compact(categories) + ",\n" +
            '"subsets":' + compact(subsets) + ",\n" +
            '"families":[\n' + ",\n".join(compact(r) for r in rows) + "\n]}\n")
    with open(OUT, "w", encoding="utf-8", newline="\n") as fh:
        fh.write(text)
    print("google_catalog.json: %d families, %d subsets, %d bytes" % (len(rows), len(subsets), os.path.getsize(OUT)))
    if skipped:
        print("left out (a name the engine cannot slug):", ", ".join(skipped))


if __name__ == "__main__":
    main()
