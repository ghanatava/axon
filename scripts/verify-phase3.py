#!/usr/bin/env python3
"""Independently verify complete h2phase3 golden frames (recorder or live BPF)."""
import base64
import collections
import json
import sys

REQUEST = [
    (0, 'SETTINGS', 0, b''), (1, 'HEADERS', 0, b'\x83'),
    (1, 'CONTINUATION', 4, b'\x87\x84'), (3, 'HEADERS', 4, b'\x83\x87\x84'),
    (1, 'DATA', 0, b'a' * 10000), (3, 'DATA', 0, b'b' * 9000),
    (1, 'DATA', 1, b'end-one'), (3, 'DATA', 1, b'end-three'),
    (0, 'PING', 0, b'pingpong'),
]
RESPONSE = [
    (0, 'SETTINGS', 0, b''), (3, 'HEADERS', 4, b'\x88'),
    (1, 'HEADERS', 4, b'\x88'), (3, 'DATA', 1, b'response-three'),
    (1, 'DATA', 1, b'response-one'), (0, 'PING', 1, b'pingpong'),
]

def verify(path):
    groups = collections.defaultdict(list)
    with open(path, encoding='utf-8') as source:
        for line in source:
            r = json.loads(line)
            assert r['pid'] > 0 and r['conn'] > 0, 'invalid endpoint identity'
            assert r['direction'] in ('read', 'write'), 'invalid direction'
            payload = base64.b64decode(r['payload'] or '', validate=True)
            assert len(payload) == r['length'], 'payload length mismatch'
            key = (r['pid'], r['conn'], r['direction'])
            assert r['sequence'] == len(groups[key]) + 1, 'wire sequence gap/reorder'
            groups[key].append((r['stream_id'], r['type'], r['flags'], payload))
    assert len(groups) == 4, f'expected four endpoint/direction groups, got {len(groups)}'
    endpoints = collections.defaultdict(dict)
    for (pid, conn, direction), frames in groups.items():
        assert frames in (REQUEST, RESPONSE), f'corrupt/incomplete frame sequence at {(pid, conn, direction)}'
        endpoints[(pid, conn)][direction] = 'request' if frames == REQUEST else 'response'
    assert len(endpoints) == 2, 'expected two TLS endpoints'
    roles = []
    for endpoint, directions in endpoints.items():
        assert set(directions) == {'read', 'write'}, f'missing direction at {endpoint}'
        assert set(directions.values()) == {'request', 'response'}, f'incorrect directions at {endpoint}'
        roles.append(directions['write'])
    assert sorted(roles) == ['request', 'response'], 'expected one client and one server'
    print('Phase 3 frames verified: 30 exact golden frames, two streams, both endpoints/directions, no payload corruption')

if __name__ == '__main__':
    if len(sys.argv) != 2:
        sys.exit('usage: verify-phase3.py FRAMES.jsonl')
    try:
        verify(sys.argv[1])
    except (AssertionError, KeyError, ValueError, OSError) as error:
        sys.exit(f'Phase 3 verification FAILED: {error}')
