# Convert alacritty ref tests (grid.json) into a compact expectation file.
# Usage: python3 -I alacritty_conv.py <refdir> <outdir>
import gzip, json, os, shutil, sys

src, out = sys.argv[1], sys.argv[2]
NAMED = ["Black", "Red", "Green", "Yellow", "Blue", "Magenta", "Cyan", "White"]

def color(c, default):
    if "Named" in c:
        n = c["Named"]
        if n == default:
            return "-"
        if n in NAMED:
            return "i%d" % NAMED.index(n)
        if n.startswith("Bright") and n[6:] in NAMED:
            return "i%d" % (8 + NAMED.index(n[6:]))
        return "N" + n
    if "Indexed" in c:
        return "i%d" % c["Indexed"]
    s = c["Spec"]
    return "r%02x%02x%02x" % (s["r"], s["g"], s["b"])

FLAGS = {"INVERSE": "R", "BOLD": "B", "ITALIC": "I", "UNDERLINE": "U1", "DOUBLE_UNDERLINE": "U2",
         "UNDERCURL": "U3", "DOTTED_UNDERLINE": "U4", "DASHED_UNDERLINE": "U5", "DIM": "D",
         "HIDDEN": "H", "STRIKEOUT": "S"}

for name in sorted(os.listdir(src)):
    d = os.path.join(src, name)
    if not os.path.isdir(d):
        continue
    size = json.load(open(os.path.join(d, "size.json")))
    cfg = json.load(open(os.path.join(d, "config.json")))
    g = json.load(open(os.path.join(d, "grid.json")))
    raw = g["raw"]
    inner, zero, vis, ln = raw["inner"], raw["zero"], g["lines"], raw["len"]  # old dumps store visible_lines-1
    lines = []
    for i in range(-(ln - vis), vis):
        pos = vis - i - 1
        row = inner[(zero + pos) % len(inner)]["inner"]
        text, styles, wrap = [], [], False
        for col, c in enumerate(row):
            fl = [f.strip() for f in c["flags"].split("|") if f.strip()]
            if "WRAPLINE" in fl:
                wrap = True
            if "WIDE_CHAR_SPACER" in fl:
                continue
            ch = c["c"]
            if "LEADING_WIDE_CHAR_SPACER" in fl or ch == "\t":
                ch = " "  # alacritty keeps tabs in cells for copying
            extra = c.get("extra") or {}
            ch += "".join(extra.get("zerowidth") or [])
            text.append(ch)
            st = color(c["fg"], "Foreground") + "/" + color(c["bg"], "Background") + "/" + "".join(
                sorted(FLAGS[f] for f in fl if f in FLAGS))
            styles.append(st)
        # Trailing default blanks are trimmed from both (not from a row
        # that wraps: its blanks are part of the line).
        while not wrap and text and text[-1] == " " and styles[-1] == "-/-/":
            text.pop(); styles.pop()
        rle = []
        for s in styles:
            if rle and rle[-1][1] == s:
                rle[-1][0] += 1
            else:
                rle.append([1, s])
        lines.append({"history": i < 0, "text": "".join(text), "wrap": wrap,
                      "style": " ".join("%d*%s" % (n, s) for n, s in rle)})
    od = os.path.join(out, name)
    os.makedirs(od, exist_ok=True)
    with gzip.GzipFile(os.path.join(od, "recording.gz"), "wb", mtime=0) as f:
        f.write(open(os.path.join(d, "alacritty.recording"), "rb").read())
    with gzip.GzipFile(os.path.join(od, "expected.json.gz"), "wb", mtime=0) as f:
        f.write(json.dumps({"cols": size["columns"], "rows": size["screen_lines"], "history": cfg["history_size"],
                            "lines": lines}, indent=0, ensure_ascii=False).encode())
