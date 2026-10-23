#!/usr/bin/env python3
"""Verify unique payload identities, not just counts, in a multi-process trace."""
import argparse
import base64
import collections
import json


def payload(message):
    data = base64.b64decode(message['data'], validate=True)
    assert data and data[0] == 10, 'not protobuf BytesValue'
    n = 0
    for i in range(1, 11):
        assert i < len(data), 'short protobuf length'
        n |= (data[i] & 127) << (7 * (i - 1))
        if data[i] < 128:
            body = data[i + 1:]
            assert len(body) == n, 'protobuf length mismatch'
            assert message['compressed'] is False and message['wire_size'] == len(data)
            token, padding = body.decode('ascii').split('|', 1)
            assert padding == 'x' * 96, 'corrupt payload padding'
            return token, len(data)
    raise AssertionError('overlong protobuf varint')


def verify(calls_path, manifest_paths, allow_loss=False, require_worker=None):
    expected = {}
    manifests = []
    for path in manifest_paths:
        m = json.load(open(path)); manifests.append(m)
        for token in m['tokens']:
            assert token not in expected, 'duplicate expected identity'
            expected[token] = (m['pid'], m['worker'])
    observed = collections.defaultdict(list)
    identities = set()
    endpoint_prefix = {}
    reused = collections.defaultdict(set)
    rows = 0
    for line in open(calls_path):
        c = json.loads(line); rows += 1
        assert c['method'] == '/axon.load.Echo/Unary' and c['status'] == 0
        assert c['status_source'] == 'grpc-status' and c['http_status'] == 200
        assert len(c['request']) == len(c['response']) == 1
        token, n = payload(c['request'][0]); reply, rn = payload(c['response'][0])
        assert token == reply and token in expected, 'misattributed or corrupt RPC'
        assert c['pid'] == expected[token][0], 'payload crossed process boundary'
        assert c['request_bytes'] == n and c['response_bytes'] == rn
        assert c['request_wire_bytes'] == n + 5 and c['response_wire_bytes'] == rn + 5
        assert c['process_start'] > 0 and c['generation'] > 0
        assert c['request_direction'] in ('read', 'write')
        if c['timing_valid']:
            assert 0 < c['started_ns'] <= c['finished_ns']
            assert c['latency_ns'] == c['finished_ns'] - c['started_ns']
        else:
            assert c['latency_ns'] == 0
        endpoint = (c['pid'], c['process_start'], c['conn'], c['generation'])
        identity = endpoint + (c['stream_id'],)
        assert identity not in identities, 'duplicate call record'
        identities.add(identity)
        group = (token.rsplit('/', 1)[0], c['request_direction'])
        assert endpoint_prefix.setdefault(endpoint, group) == group, 'cross-connection payload contamination'
        observed[token].append(c['request_direction'])
        reused[endpoint[:3]].add(endpoint[3])
    for token, (_, worker) in expected.items():
        directions = observed[token]
        assert len(directions) <= 2 and len(directions) == len(set(directions)), 'duplicate endpoint observation'
        if not allow_loss or worker == require_worker:
            assert sorted(directions) == ['read', 'write'], f'missing {token}'
    if require_worker:
        assert any(m['worker'] == require_worker for m in manifests), 'missing recovery worker manifest'
    print(f'Phase5 verified: {rows} records, {len(expected)} logical RPCs, {len(manifests)} processes, '
          f'{len(endpoint_prefix)} TLS incarnations, {sum(len(g)-1 for g in reused.values())} pointer reuses; zero misattribution')
    return rows


if __name__ == '__main__':
    p = argparse.ArgumentParser()
    p.add_argument('--calls', required=True)
    p.add_argument('--manifests', nargs='+', required=True)
    p.add_argument('--allow-loss', action='store_true')
    p.add_argument('--require-worker')
    a = p.parse_args()
    verify(a.calls, a.manifests, a.allow_loss, a.require_worker)
