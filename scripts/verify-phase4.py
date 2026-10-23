#!/usr/bin/env python3
"""Independent golden check of grpcphase4 call JSONL (live or replay)."""
import base64
import collections
import json
import sys

def varint(data, i):
    value = 0
    for shift in range(0, 70, 7):
        assert i < len(data), 'short protobuf varint'
        b = data[i]; i += 1
        value |= (b & 127) << shift
        if b < 128:
            return value, i
    raise AssertionError('overlong varint')

def message(m):
    data = base64.b64decode(m['data'], validate=True)
    assert data and data[0] == 10, 'expected protobuf BytesValue field1'
    n, i = varint(data, 1)
    assert i + n == len(data), 'wrong protobuf payload size'
    assert m['wire_size'] >= 0
    return data[i:], len(data)

def check(path):
    records = [json.loads(line) for line in open(path) if line.strip()]
    assert len(records) == 10, f'expected10 calls, got {len(records)}'
    endpoints = collections.defaultdict(list)
    ids = set()
    for c in records:
        ident = (c['pid'], c['conn'], c['stream_id'])
        assert ident not in ids and all(ident), 'duplicate/missing identity'
        ids.add(ident); endpoints[ident[:2]].append(c)
        assert c['request_direction'] in ('read', 'write')
        assert c['http_status'] == 200 and c['status_source'] == 'grpc-status'
        assert c['timing_valid'] and 0 < c['started_ns'] <= c['finished_ns']
        assert c['latency_ns'] == c['finished_ns'] - c['started_ns']
        for side in ('request', 'response'):
            messages = c[side] or []
            assert c[side + '_bytes'] == sum(message(m)[1] for m in messages)
            assert c[side + '_wire_bytes'] == sum(5 + m['wire_size'] for m in messages)
            for m in messages:
                if not m['compressed']:
                    assert m['wire_size'] == message(m)[1]
                else:
                    assert m['encoding'] == 'gzip'
    assert len(endpoints) == 2, 'expected both TLS endpoints'
    for calls in endpoints.values():
        assert len(calls) == 5 and len({c['request_direction'] for c in calls}) == 1
        seen = set()
        for c in calls:
            req = [message(m)[0] for m in (c['request'] or [])]
            res = [message(m)[0] for m in (c['response'] or [])]
            if c['method'] == '/axon.demo.Echo/Bidi':
                assert req == res == [b'one', b'two' * 1000, b'three']
                key = 'bidi'; assert c['status'] == 0
            else:
                assert c['method'] == '/axon.demo.Echo/Unary' and len(req) == 1
                payload = req[0]
                assert payload in [b'hello-phase4', b'large-message-' * 1000, b'compress-me-' * 700, b'fail']
                key = payload
                if payload == b'fail':
                    assert c['status'] == 3 and c['status_message'] == 'bad input + details' and res == []
                else:
                    assert c['status'] == 0 and res == req
                    compressed = payload == b'compress-me-' * 700
                    assert all(m['compressed'] == compressed for m in c['request'] + c['response'])
            assert key not in seen, 'duplicate fixture call'
            seen.add(key)
        assert len(seen) == 5
    print('Phase4 golden verified:10 exact calls,2 endpoints,unary,bidi,gzip,status,payloads,sizes,timing')
if __name__ == '__main__':
    check(sys.argv[1])
