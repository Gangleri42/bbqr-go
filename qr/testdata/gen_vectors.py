#!/usr/bin/env python3
#
# (c) Copyright 2026 by Coinkite Inc. This file is in the public domain.
#
# Generate vectors.json for package qr from pyqrcode: for each case the
# text, level, version, the mode and mask pyqrcode chose, and a digest
# of its module matrix (rows of 0/1 characters, joined). Small symbols
# also carry the full matrix so a failure is readable.
#
#   ../../../python/ENV/bin/python gen_vectors.py > vectors.json
#
import hashlib, json, random
import pyqrcode
from pyqrcode import tables

rng = random.Random(2026)
ALNUM = '0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ $%*+-./:'

def digest(code):
    rows = [''.join(str(b) for b in row) for row in code]
    return hashlib.sha256('\n'.join(rows).encode()).hexdigest(), rows

cases = []
def add(name, text, level, version):
    q = pyqrcode.create(text, error=level, version=version)
    h, rows = digest(q.code)
    c = dict(name=name, text=text, level=level, version=version,
             mode=q.mode, mask=q.builder.best_mask, sha256=h)
    if version <= 3:
        c['rows'] = rows
    cases.append(c)

def alnum(n):
    return ''.join(rng.choice(ALNUM) for _ in range(n))

# Every version at level L: a BBQr-shaped part filling the symbol, and a
# shorter one leaving room for padding codewords.
for v in range(1, 41):
    cap = tables.data_capacity[v]['L'][2]
    head = 'B$HP' + '%02d' % (v % 100) + '00'
    body = ''.join(rng.choice('0123456789ABCDEF') for _ in range(cap - len(head)))
    add('v%d-L-full' % v, head + body, 'L', v)
    add('v%d-L-part' % v, head + body[:max(1, (cap - len(head)) * 3 // 5)], 'L', v)

# Other levels, other modes.
for v in (1, 5, 20, 40):
    for lvl in 'MQH':
        cap = tables.data_capacity[v][lvl][2]
        add('v%d-%s-alnum' % (v, lvl), alnum(cap), lvl, v)
for v in (1, 2, 9, 10, 26, 27, 40):
    cap = tables.data_capacity[v]['L'][1]
    add('v%d-L-numeric' % v, ''.join(rng.choice('0123456789') for _ in range(cap)), 'L', v)
    cap = tables.data_capacity[v]['L'][4]
    add('v%d-L-bytes' % v, ''.join(chr(rng.randrange(0x21, 0x7f)) for _ in range(cap)), 'L', v)
add('spec-part', 'B$HB03000102', 'L', 1)
add('lowercase-bytes', 'seedhammer', 'L', 1)
add('one-char', 'A', 'L', 1)

print(json.dumps(dict(
    version_bits={v: tables.version_pattern[v] for v in range(7, 41)},
    format_bits={lvl: tables.type_bits[lvl] for lvl in 'LMQH'},
    cases=cases,
), indent=1))
