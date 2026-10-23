#!/usr/bin/env python3
"""Drive traffic and pod/process lifecycle through the real Kubernetes API."""
import json,os,pathlib,re,subprocess,time,urllib.request
root=pathlib.Path('build/phase7-evaluation');root.mkdir(parents=True,exist_ok=True)
def kubectl(*args):return subprocess.check_output(['kubectl',*args],text=True)
def pods(ns):return json.loads(kubectl('-n',ns,'get','pods','-o','json'))['items']
def agent_pod():return next(p['metadata']['name'] for p in pods('axon') if p['metadata']['labels'].get('app')=='axon' and p['status']['phase']=='Running')
agent=agent_pod()
proxylog=open(root/'proxy.log','w');proxy=subprocess.Popen(['kubectl','proxy','--port=0'],stdout=proxylog,stderr=proxylog)
def until(fn,description,seconds=60):
    end=time.monotonic()+seconds
    while time.monotonic()<end:
        try:
            result=fn()
            if result:return result
        except (OSError,ValueError,subprocess.CalledProcessError):pass
        time.sleep(.25)
    raise AssertionError('Timed out: '+description)
try:
    def proxy_url():
        match=re.search(r'127\.0\.0\.1:(\d+)',(root/'proxy.log').read_text())
        return 'http://127.0.0.1:'+match[1] if match else None
    base=until(proxy_url,'kubectl proxy startup')
    def request(path,post=False):return urllib.request.urlopen(urllib.request.Request(base+path,data=b'' if post else None),timeout=30).read().decode()
    agentpath=f'/api/v1/namespaces/axon/pods/{agent}:9108/proxy/'
    def metrics():return request(agentpath+'metrics')
    def gauge(name):
        text=metrics()
        return sum(float(line.split()[-1]) for line in text.splitlines() if line.startswith(name+' ') or line.startswith(name+'{'))
    def logs():return kubectl('-n','axon','logs',agent)
    def attached(pod,minimum=1):return len(re.findall(r'discovery attached pid=\d+ pod=axon-demo/'+re.escape(pod)+r' container=echo',logs()))>=minimum
    def workload(mode):return next(p for p in pods('axon-demo') if p['metadata']['labels'].get('app')=='echo-'+mode and not p['metadata'].get('deletionTimestamp') and p['status']['phase']=='Running')
    until(lambda:gauge('axon_discovery_active_targets')==2,'two automatically attached Go targets')
    assert gauge('axon_discovery_candidate_processes')>=3,'non-Go negative fixture absent'
    batches=[];identities=[]
    def run(p):
        name=p['metadata']['name'];until(lambda:attached(name),'attachment for '+name)
        # Independently check the traced host PID cgroup against API container ID.
        matches=re.findall(r'discovery attached pid=(\d+) pod=axon-demo/'+re.escape(name)+r' container=echo identity=(\S+)',logs())
        pid,identity=matches[-1];cid=p['status']['containerStatuses'][0]['containerID'].split('://')[1]
        cgroup=kubectl('-n','axon','exec',agent,'--','cat','/proc/'+pid+'/cgroup');assert cid in cgroup,(cid,cgroup)
        identities.append(dict(pod=name,pid=int(pid),identity=identity,container_id=cid,cgroup=cgroup))
        result=json.loads(request(f'/api/v1/namespaces/axon-demo/pods/{name}:19090/proxy/run?count=10',True))
        assert result['pod']==name and len(result['calls'])==10
        batches.append(dict(workload=p,result=result,pid=int(pid)))
        return pid
    regular=workload('unstripped');old=workload('stripped');run(regular);run(old)
    kubectl('-n','axon-demo','rollout','restart','deployment/echo-stripped');kubectl('-n','axon-demo','rollout','status','deployment/echo-stripped','--timeout=120s')
    replacement=until(lambda:workload('stripped') if workload('stripped')['metadata']['uid']!=old['metadata']['uid'] else None,'new stripped pod')
    until(lambda:gauge('axon_discovery_active_targets')==2 and attached(replacement['metadata']['name']),'replacement attached and old pod detached')
    pid=run(replacement)
    # exec replaces the Go runtime without changing PID/container/pod identity.
    request(f'/api/v1/namespaces/axon-demo/pods/{replacement["metadata"]["name"]}:19090/proxy/restart-runtime',True)
    until(lambda:attached(replacement['metadata']['name'],2),'same-PID exec reattachment')
    assert run(replacement)==pid,'self exec unexpectedly changed PID'
    assert identities[-1]['identity']!=identities[-2]['identity'],'exec identity not refreshed'
    until(lambda:gauge('axon_grpc_requests_total')==80,'all 80 endpoint observations')
    (root/'active.metrics').write_text(metrics());(root/'batches.json').write_text(json.dumps(batches,indent=2)+'\n');(root/'identities.json').write_text(json.dumps(identities,indent=2)+'\n')
    # Exercise real detach and map/worker cleanup, leaving only the non-Go pod.
    kubectl('-n','axon-demo','scale','deployment/echo-unstripped','deployment/echo-stripped','--replicas=0')
    until(lambda:gauge('axon_discovery_active_targets')==0 and gauge('axon_discovery_resident_workers')==0,'all target worker processes exited')
    until(lambda:gauge('axon_capture_active_connections')==0 and gauge('axon_capture_retained_bytes')==0,'parser state released')
    text=logs();(root/'agent.log').write_text(text)
    (root/'calls.jsonl').write_text('\n'.join(line for line in text.splitlines() if line.startswith('{"pid":'))+'\n')
    (root/'final.metrics').write_text(metrics())
    (root/'pods.json').write_text(kubectl('get','pods','-A','-o','json'))
    allowed=kubectl('auth','can-i','list','pods','--as=system:serviceaccount:axon:axon','--all-namespaces').strip();assert allowed=='yes'
    denied=subprocess.run(['kubectl','auth','can-i','get','secrets','--as=system:serviceaccount:axon:axon','--all-namespaces'],capture_output=True,text=True);assert denied.stdout.strip()=='no'
    (root/'rbac.txt').write_text('list pods: '+allowed+'\nget secrets: '+denied.stdout.strip()+'\n')
    print('Cluster lifecycle passed: regular/stripped discovery, pod replacement, same-PID exec, non-Go exclusion, detach and RBAC')
finally:
    proxy.terminate();proxy.wait(timeout=10);proxylog.close()
