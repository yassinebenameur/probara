"""Run inside a 4-CPU/4-GiB container with --network none.

Real HTTP requests target only loopback. Soak uses 500 single decisions;
20-monitor RCA and 50-pair batches have separate warm latency measurements.
"""
import argparse
import json
import math
import os
from pathlib import Path
import statistics
import subprocess
import sys
import time
import urllib.request
from probara_ai.contract import options

TOKEN = "isolated-cpu-benchmark-internal-token"


def request(path, body=None):
    payload = json.dumps(body).encode() if body else None
    r = urllib.request.Request("http://127.0.0.1:8090" + path, data=payload, headers={"Authorization": "Bearer " + TOKEN, "Content-Type": "application/json"})
    with urllib.request.urlopen(r, timeout=90) as response:
        return json.load(response)


def fixture(task, size):
    decisions = []
    for i in range(size):
        evidence = {"checks": 12+i, "failures": 4+i%4, "dns_failures": 4+i%4, "max_dns_ms": 250+i*11} if task == "rca" else {"co_fires": 5+i%8, "a_leads_seconds": 30+i*5, "reverse_co_fires": 0, "shared_service": True, "shared_group": bool(i%2), "naming_signal": False, "common_upstream": False}
        decisions.append({"id": f"bench_{i}", "task": task, "evidence": evidence, "choices": options(task)})
    return {"decisions": decisions}


def run(output):
    # Record and check kernel-enforced limits, rather than labeling an
    # unconstrained host run as the required production resource benchmark.
    cpu_max=Path('/sys/fs/cgroup/cpu.max').read_text().split()
    memory_max=Path('/sys/fs/cgroup/memory.max').read_text().strip()
    if cpu_max[0]=='max' or float(cpu_max[0])/float(cpu_max[1])!=4 or memory_max=='max' or int(memory_max)!=4*1024**3:
        raise ValueError('Benchmark requires a cgroup with exactly 4 CPUs and 4 GiB')
    os.environ.update(LOCAL_AI_TOKEN=TOKEN, LOCAL_AI_THREADS="4")
    started = time.perf_counter()
    child = subprocess.Popen([sys.executable, "-m", "uvicorn", "probara_ai.server:create_app", "--factory", "--host", "127.0.0.1", "--port", "8090", "--workers", "1", "--no-access-log"], stdout=subprocess.DEVNULL, stderr=sys.stderr)
    peak = 0
    def memory():
        nonlocal peak
        for line in Path(f"/proc/{child.pid}/status").read_text().splitlines():
            if line.startswith("VmHWM:"): peak = max(peak, int(line.split()[1])*1024)
    try:
        while True:
            if child.poll() is not None: raise RuntimeError("CPU service exited during cold startup")
            try: identity = request("/readyz")["model"]; break
            except Exception:
                if time.perf_counter()-started > 90: raise
                time.sleep(.2)
        report = {"model": identity, "cpu_limit": 4, "memory_limit_bytes": 4*1024**3, "network": "none", "cold_start_seconds": time.perf_counter()-started}
        for task, size, repeats in (("rca",20,10),("dependency",50,5)):
            request("/v1/decisions", fixture(task,size))
            durations = []
            for _ in range(repeats):
                before=time.perf_counter();request("/v1/decisions",fixture(task,size));durations.append(time.perf_counter()-before);memory()
            report[f"warm_{task}"]={"batch_size":size,"requests":repeats,"p95_seconds":sorted(durations)[math.ceil(.95*len(durations))-1],"max_seconds":max(durations),"median_seconds":statistics.median(durations)}
            print(json.dumps(report[f"warm_{task}"]),flush=True)
        before=time.perf_counter()
        for i in range(500):
            response=request("/v1/decisions",fixture("rca" if i%2==0 else "dependency",1))
            assert len(response["results"])==1
            memory()
            if i%100==99:print(f"Soak: {i+1}/500",flush=True)
        report["soak"]={"requests":500,"batch_size":1,"failures":0,"seconds":time.perf_counter()-before}
        report["peak_service_rss_bytes"]=peak
        cgroup_peak=Path("/sys/fs/cgroup/memory.peak")
        if cgroup_peak.exists():report["peak_container_memory_bytes"]=int(cgroup_peak.read_text())
        report["resource_gates_passed"]=report["warm_rca"]["p95_seconds"]<30 and report["warm_dependency"]["p95_seconds"]<60 and peak<4*1024**3 and report.get("peak_container_memory_bytes",4*1024**3)<4*1024**3
        output.write_text(json.dumps(report,indent=2)+"\n")
        return 0 if report["resource_gates_passed"] else 2
    finally:
        child.terminate()
        child.wait(timeout=20)


if __name__=="__main__":
    p=argparse.ArgumentParser();p.add_argument("--output",type=Path,required=True)
    raise SystemExit(run(p.parse_args().output))
