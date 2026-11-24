#!/usr/bin/env bash
# Measures log line → webhook latency on the current cluster (Riffle must be deployed).
# A pod logs one error line per trial with its write time; a webhook sink reports arrival − write time.
# Usage: hack/bench-latency.sh [trials]
set -euo pipefail

N=${1:-20}
NS=riffle-bench
OP_NS=riffle-system
PY=python:3.13-alpine

cleanup() {
  kubectl delete ns "$NS" --ignore-not-found --wait=false >/dev/null
  kubectl -n "$OP_NS" delete secret riffle-webhook --ignore-not-found >/dev/null
}
trap cleanup EXIT

kubectl create ns "$NS" >/dev/null
kubectl -n "$OP_NS" create secret generic riffle-webhook --from-literal=url="http://sink.$NS.svc:8080" >/dev/null

kubectl apply -n "$NS" -f - >/dev/null <<EOF
apiVersion: v1
kind: Pod
metadata:
  name: sink
  labels: {app: sink}
spec:
  containers:
  - name: sink
    image: $PY
    command: [python, -u, -c]
    args:
    - |
      import json, time
      from http.server import BaseHTTPRequestHandler, HTTPServer
      class H(BaseHTTPRequestHandler):
          def do_POST(self):
              now = time.time()
              a = json.loads(self.rfile.read(int(self.headers['Content-Length'])))
              if a['status'] == 'firing':
                  print('latency_ms', round((now - json.loads(a['sample'])['t']) * 1000), flush=True)
              self.send_response(204); self.end_headers()
          def log_message(self, *args): pass
      HTTPServer(('', 8080), H).serve_forever()
---
apiVersion: v1
kind: Service
metadata:
  name: sink
spec:
  selector: {app: sink}
  ports: [{port: 8080}]
EOF

for i in $(seq "$N"); do
  cat <<EOF
apiVersion: riffle.jhchen.dev/v1alpha1
kind: LogAlert
metadata:
  name: bench-$i
spec:
  selector: {matchLabels: {app: emitter}}
  where: trial=$i
  threshold: {count: 1, window: 1m}
---
EOF
done | kubectl apply -n "$NS" -f - >/dev/null

kubectl -n "$NS" wait pod/sink --for=condition=Ready --timeout=120s >/dev/null

# Trials are 2s apart so each one is a separate push.
kubectl apply -n "$NS" -f - >/dev/null <<EOF
apiVersion: v1
kind: Pod
metadata:
  name: emitter
  labels: {app: emitter}
spec:
  restartPolicy: Never
  containers:
  - name: emitter
    image: $PY
    command: [python, -u, -c]
    args:
    - |
      import json, time
      time.sleep(10)  # let the agent start following this container
      for i in range(1, $N + 1):
          print(json.dumps({'level': 'error', 'trial': i, 't': time.time()}), flush=True)
          time.sleep(2)
EOF

echo "Running $N trials..." >&2
deadline=$((SECONDS + 60 + 3 * N))
until [ "$(kubectl -n "$NS" logs sink | grep -c latency_ms)" -ge "$N" ] || [ $SECONDS -gt $deadline ]; do
  sleep 1
done

kubectl -n "$NS" logs sink | awk '/latency_ms/ {print $2}' | sort -n | awk -v n="$N" '
  { v[NR] = $1 }
  END {
    if (NR < n) printf "only %d of %d alerts arrived\n", NR, n
    if (NR == 0) exit 1
    printf "alerts %d  p50 %d ms  p90 %d ms  max %d ms\n", NR, v[int((NR + 1) * 0.5)], v[int((NR + 1) * 0.9) > NR ? NR : int((NR + 1) * 0.9)], v[NR]
  }'
