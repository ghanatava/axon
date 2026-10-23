#!/usr/bin/env python3
"""Independent expected-payload/identity/metric checks for the kind workload."""
import collections,json,pathlib,re,math
from importlib.machinery import SourceFileLoader
phase4=SourceFileLoader('phase4','scripts/verify-phase4.py').load_module()
phase6=SourceFileLoader('phase6','scripts/verify-phase6.py').load_module()
root=pathlib.Path('build/phase7-evaluation');batches=json.loads((root/'batches.json').read_text());calls=[json.loads(v) for v in (root/'calls.jsonl').read_text().splitlines() if v]
assert len(batches)==4 and len(calls)==80,(len(batches),len(calls))
expected={};pod_counts=collections.Counter()
for b in batches:
    p=b['workload'];meta=p['metadata'];container=p['status']['containerStatuses'][0]
    for c in b['result']['calls']:
        assert c['token'] not in expected,'fixture token reused across exec/restart'
        expected[c['token']]=(c['status'],meta['name'],meta['uid'],meta['namespace'],meta['labels']['app.kubernetes.io/name'],container['containerID'].split('://')[1],b['pid'])
        pod_counts[meta['name']]+=1
seen=collections.Counter();identities=set();want_metrics=collections.Counter()
for c in calls:
    req=c['request'];assert len(req)==1;token=phase4.message(req[0])[0].decode();status,pod,uid,namespace,service,cid,pid=expected[token]
    assert (c['status'],c['pod'],c['pod_uid'],c['namespace'],c['service'],c['container_id'],c['pid'])==(status,pod,uid,namespace,service,cid,pid),c
    assert c['method']=='/axon.phase7.Echo/Unary' and c['http_status']==200 and c['status_source']=='grpc-status'
    role='server' if c['request_direction']=='read' else 'client';assert c['request_direction'] in ('read','write')
    assert c['container']=='echo' and c['node']=='axon-phase7-control-plane'
    if status==0:assert [phase4.message(m)[0].decode() for m in c['response']]==[token]
    else:assert status==3 and not c['response']
    identity=(c['pid'],c['process_start'],c['generation'],c['conn'],c['stream_id']);assert identity not in identities;identities.add(identity)
    seen[token,role]+=1;want_metrics[pod,service,namespace,role,str(status)]+=1
for token in expected:assert seen[token,'server']==seen[token,'client']==1,token
actual=collections.Counter()
samples={}
for line in (root/'final.metrics').read_text().splitlines():
    if not line.startswith('axon_grpc_'):continue
    raw,value=line.split('} ');name,raw=raw.split('{',1);labels=phase6.labels(raw)
    samples[name,tuple(sorted(labels.items()))]=float(value)
groups=collections.defaultdict(list)
for c in calls:groups[c['pod'],c['service'],c['namespace'],c['request_direction'],str(c['status'])].append(c)
for (pod,service,namespace,direction,status),group in groups.items():
    labels=dict(pod=pod,service=service,namespace=namespace,role='server' if direction=='read' else 'client',grpc_status=status,method='/axon.phase7.Echo/Unary')
    def sample(name,**extra):return samples['axon_'+name,tuple(sorted((labels|extra).items()))]
    for side in ('request','response'):assert sample('grpc_'+side+'_bytes_total')==sum(c[side+'_bytes'] for c in group)
    timed=[c['latency_ns']/1e9 for c in group if c['timing_valid']]
    assert sample('grpc_request_duration_seconds_count')==len(timed)
    assert math.isclose(sample('grpc_request_duration_seconds_sum'),sum(timed),rel_tol=1e-9,abs_tol=1e-12)
    for (name,items),value in samples.items():
        lab=dict(items)
        if name=='axon_grpc_request_duration_seconds_bucket' and all(lab.get(k)==v for k,v in labels.items()):assert value==sum(s<=float(lab['le']) for s in timed)

for line in (root/'final.metrics').read_text().splitlines():
    if line.startswith('axon_grpc_requests_total{'):
        raw,value=line.split('} ');labels=phase6.labels(raw.split('{',1)[1]);actual[labels['pod'],labels['service'],labels['namespace'],labels['role'],labels['grpc_status']]=int(float(value))
    if line.startswith('axon_discovery_exec_transitions_total{'):assert float(line.split()[-1])==1,line
    if line.startswith('axon_discovery_resident_workers{'):assert float(line.split()[-1])==0,line
    if line.startswith('axon_capture_errors_total{'):assert float(line.split()[-1])==0,line
    if line.startswith('axon_discovery_failures_total{'):assert float(line.split()[-1])==0,line
assert actual==want_metrics,(actual,want_metrics)
text=(root/'agent.log').read_text();assert 'discovery kernel exec guard stopped' in text,'kernel exec guard was not exercised';assert 'layout=release layout (stripped)' in text and 'layout=DWARF' in text
print('Phase7 verified: 40 RPCs / 80 endpoint records across three pod identities; exact bytes/status/PID/pod/service/container metrics, same-PID exec isolated, zero capture errors')
