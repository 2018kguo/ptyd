#!/bin/sh
# Creates the project docs/demo.tape records in: a CLI and twelve test suites,
# one of which fails (int() truncates 19.99 * 100 to 1998).
set -e
rm -rf "$1" && mkdir -p "$1" && cd "$1"
cat > todo.py <<'PY'
import argparse

p = argparse.ArgumentParser(prog="todo")
p.add_argument("item", nargs="*")
args = p.parse_args()
print("added:", " ".join(args.item))
PY
svc() { mkdir -p "$1"; printf '%s\n' "$2" > "$1/$1.py"; printf 'import unittest\nfrom %s import *\n\n\nclass Test(unittest.TestCase):\n%s\n' "$1" "$3" > "$1/test_$1.py"; }
svc api 'def route(path): return path.strip("/").split("/")' '    def test_route(self): self.assertEqual(route("/a/b/"), ["a", "b"])'
svc auth 'def bearer(h): return h.removeprefix("Bearer ")' '    def test_bearer(self): self.assertEqual(bearer("Bearer x"), "x")'
svc billing 'def cents(dollars): return int(dollars * 100)' '    def test_cents(self): self.assertEqual(cents(19.99), 1999)'
svc cart 'def total(prices): return sum(prices)' '    def test_total(self): self.assertEqual(total([1, 2, 3]), 6)'
svc email 'def domain(addr): return addr.rsplit("@", 1)[1]' '    def test_domain(self): self.assertEqual(domain("a@b.io"), "b.io")'
svc search 'def terms(q): return q.lower().split()' '    def test_terms(self): self.assertEqual(terms("Red Shoe"), ["red", "shoe"])'
svc users 'def initials(n): return "".join(w[0] for w in n.split())' '    def test_initials(self): self.assertEqual(initials("Ada Lovelace"), "AL")'
svc inventory 'def in_stock(n): return n > 0' '    def test_in_stock(self): self.assertFalse(in_stock(0))'
svc notify 'def plural(n, w): return w if n == 1 else w + "s"' '    def test_plural(self): self.assertEqual(plural(2, "item"), "items")'
svc orders 'def latest(ids): return max(ids)' '    def test_latest(self): self.assertEqual(latest([3, 9, 4]), 9)'
svc reports 'def pct(a, b): return 100 * a // b' '    def test_pct(self): self.assertEqual(pct(1, 4), 25)'
svc web 'def slug(t): return "-".join(t.lower().split())' '    def test_slug(self): self.assertEqual(slug("Hello World"), "hello-world")'
