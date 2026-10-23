"""Check both hook directions, stable call identities, and capture health."""
import re
import sys
from collections import defaultdict

text = open(sys.argv[1]).read()
preface = r'PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n'
seen = defaultdict(set)
connections = set()
byte_counts = defaultdict(int)
for line in text.splitlines():
    match = re.match(r'pid=(\d+) tid=(\d+) goid=(\d+) conn=(0x[0-9a-f]+) (read|write) n=(\d+) captured=(\d+) data=(.*)', line)
    if not match:
        continue
    pid, tid, goid, conn, direction, total, captured, data = match.groups()
    assert int(goid) > 0 and int(conn, 16) > 0, line
    assert 0 < int(captured) <= min(int(total), 4096), line
    connections.add((pid, conn))
    byte_counts[(pid, conn, direction)] += int(total)
    if preface in data:
        seen[(pid, conn)].add(direction)
assert len(connections) == 8, f'expected 8 endpoint connections, got {len(connections)}'
assert len(seen) == 8 and all(d == {'read', 'write'} for d in seen.values()), f'missing preface in endpoint read/write: {dict(seen)}'
expected = 10 * (24 + len(b'axon-phase2-plaintext') + 8192)
assert len(byte_counts) == 16 and all(n == expected for n in byte_counts.values()), f'byte accounting mismatch: {dict(byte_counts)}'
for label in ('memory/identity errors', 'map errors', 'ring drops'):
    assert re.search(re.escape(label) + r': 0\b', text), f'nonzero or missing {label}'
assert re.search(r'truncated calls: [1-9]\d*\b', text), 'large-buffer truncation case not exercised'
print('Verified plaintext preface in both directions on all 8 TLS endpoints; no capture errors or drops.')
