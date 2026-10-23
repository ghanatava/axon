#!/usr/bin/env python3
"""Verify provisioned dashboard queries against actual Prometheus scrape history."""
import json,math,pathlib,time,urllib.parse,urllib.request
root=pathlib.Path('build/phase6-evaluation');root.mkdir(parents=True,exist_ok=True)
def get(url):return json.load(urllib.request.urlopen(url,timeout=10))
base='http://127.0.0.1:9090/api/v1/'
end=time.time();results={}
dashboard=get('http://127.0.0.1:3000/api/dashboards/uid/axon-grpc')['dashboard']
assert dashboard['uid']=='axon-grpc' and len(dashboard['panels'])==13
for panel in dashboard['panels']:
    expr=panel['targets'][0]['expr']
    for k,v in {'service':'phase6-echo','namespace':'test','pod':'fixture','role':'server','method':'.*','__rate_interval':'20s'}.items():expr=expr.replace('$'+k,v)
    data=get(base+'query_range?'+urllib.parse.urlencode(dict(query=expr,start=end-180,end=end,step=2)))
    assert data['status']=='success',(panel['title'],data)
    result=data['data']['result'];assert result,(panel['title'],'no scraped data')
    finite=[float(v) for r in result for _,v in r['values'] if math.isfinite(float(v))]
    assert finite,(panel['title'],'no finite values')
    if panel['title'] in ['Completed RPCs / second','Error rate','p95 observed latency']:assert max(finite)>0,(panel['title'],'no live signal')
    results[panel['title']]=data
# Cumulative totals are endpoint observations: 100 server / 100 client, 20 errors each.
expr='sum by (role, grpc_status) (last_over_time(axon_grpc_requests_total{service="phase6-echo"}[5m]))'
data=get(base+'query?'+urllib.parse.urlencode({'query':expr}))
counts={(v['metric']['role'],v['metric']['grpc_status']):float(v['value'][1]) for v in data['data']['result']}
assert counts=={('client','0'):80,('client','3'):20,('server','0'):80,('server','3'):20},counts
# Exercise the datasource through Grafana too, not only direct Prometheus queries.
proxy=get('http://127.0.0.1:3000/api/datasources/proxy/uid/axon-prometheus/api/v1/query?'+urllib.parse.urlencode({'query':expr}))
assert proxy['status']=='success' and len(proxy['data']['result'])==4
(root/'dashboard-queries.json').write_text(json.dumps(results,indent=2)+'\n')
(root/'prometheus-counts.json').write_text(json.dumps(data,indent=2)+'\n')
(root/'grafana-dashboard.json').write_text(json.dumps(dashboard,indent=2)+'\n')
print('Phase6 dashboard verified: 13 valid panels with scraped data; live rate/errors/latency; Grafana datasource query and exact endpoint totals')
