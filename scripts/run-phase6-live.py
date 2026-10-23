#!/usr/bin/env python3
"""Own and clean up a live agent; optionally run privileged capture via Docker."""
import json,os,pathlib,re,subprocess,sys,time,urllib.request
mode=sys.argv[1];repo=pathlib.Path.cwd();root=repo/'build/phase6'
name='axon-phase6-capture-'+str(os.getpid())
docker=os.environ.get('AXON_AGENT_DOCKER')=='1'
args=[str(repo/'build/axon-agent'),'-bin',str(root/('fixture-'+mode)),'-mode','grpc','-metrics-listen','127.0.0.1:9108','-metrics-output',str(root/(mode+'.metrics')),'-service','phase6-echo','-namespace','test','-pod','fixture']
if docker:
    args=['docker','run','--rm','--name',name,'--privileged','--pid=host','--network=host','-v',f'{repo}:{repo}','-v','/sys:/sys:ro','-w',str(repo),'debian:bookworm-slim']+args
with open(root/(mode+'.calls'),'w') as calls,open(root/(mode+'.log'),'w') as log:
    p=subprocess.Popen(args,stdout=calls,stderr=log)
    try:
        for _ in range(200):
            text=(root/(mode+'.log')).read_text()
            if 'capture ready' in text:break
            if p.poll() is not None:raise RuntimeError(text)
            time.sleep(.05)
        else:raise RuntimeError('capture readiness timed out: '+text)
        # Multiple scrapes while counters change, including explicit gRPC errors.
        for n in range(20):
            subprocess.run([str(root/('fixture-'+mode))],check=True,stdout=subprocess.DEVNULL,timeout=30)
            time.sleep(.35)
        for _ in range(100):
            text=urllib.request.urlopen('http://127.0.0.1:9108/metrics',timeout=3).read().decode()
            total=sum(float(line.split()[-1]) for line in text.splitlines() if line.startswith('axon_grpc_requests_total{'))
            if total==200:break
            time.sleep(.05)
        assert total==200,total
        (root/(mode+'.http.metrics')).write_text(text)
        # Keep endpoint available through another Prometheus scrape.
        time.sleep(3)
        if docker:subprocess.run(['docker','stop','--time','5',name],check=True,stdout=subprocess.DEVNULL)
        else:p.terminate()
        assert p.wait(timeout=10)==0,(root/(mode+'.log')).read_text()
        text=(root/(mode+'.log')).read_text()
        for label in ['memory/identity errors','map errors','ring drops','truncated calls','overlapping calls','unattributed errors']:
            assert f'{label}: 0' in text,text
        stats=json.loads(re.search(r'runtime_stats (\{.*\})',text)[1])
        assert stats['invalidated']==stats['incomplete']==stats['active']==0,stats
    finally:
        if docker:subprocess.run(['docker','stop','--time','2',name],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
        if p.poll() is None:p.terminate();p.wait(timeout=10)
