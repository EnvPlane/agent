#!/usr/bin/env bash
# Candidate-only operator preparation. Never called by the Agent runtime.
set -euo pipefail
umask 077
task_dir="${1:?isolated output directory required}"
test -d "$task_dir"
context=bethunder-policy-candidate
openssl req -x509 -newkey ed25519 -nodes -keyout "$task_dir/ca.key" -out "$task_dir/ca.crt" -days 2 -subj /CN=envplane-candidate-finops-ca -addext basicConstraints=critical,CA:TRUE -addext keyUsage=critical,keyCertSign,cRLSign >/dev/null 2>&1
kubectl --context "$context" create namespace envplane-telemetry --dry-run=client -o yaml | kubectl --context "$context" apply -f - >/dev/null
issue_tls() {
  local namespace="$1" service="$2" secret="$3"
  local prefix="$task_dir/$namespace-$service"
  openssl req -new -newkey ed25519 -nodes -keyout "$prefix.key" -out "$prefix.csr" -subj "/CN=$service.$namespace.svc" -addext "subjectAltName=DNS:$service.$namespace.svc,DNS:$service.$namespace.svc.cluster.local" >/dev/null 2>&1
  openssl x509 -req -in "$prefix.csr" -CA "$task_dir/ca.crt" -CAkey "$task_dir/ca.key" -CAcreateserial -out "$prefix.crt" -days 2 -copy_extensions copy >/dev/null 2>&1
  kubectl --context "$context" -n "$namespace" create secret tls "$secret" --cert="$prefix.crt" --key="$prefix.key" --dry-run=client -o yaml | kubectl --context "$context" apply -f - >/dev/null
}
for namespace in app-backend app2-backend envplane-pr-e2e-ui-full-652-1007652; do
  issue_tls "$namespace" finops-pvc-usage finops-pvc-tls
done
issue_tls envplane-telemetry finops-prometheus finops-server-tls
for namespace in envplane-telemetry envplane-system; do
  kubectl --context "$context" -n "$namespace" create secret generic finops-exporter-ca --from-file=ca.crt="$task_dir/ca.crt" --dry-run=client -o yaml | kubectl --context "$context" apply -f - >/dev/null
done
minikube -p "$context" ssh -- 'sudo cat /var/lib/kubelet/pki/kubelet.crt' | tr -d '\r' > "$task_dir/kubelet.crt"
openssl x509 -in "$task_dir/kubelet.crt" -noout -checkend 3600 >/dev/null
kubectl --context "$context" -n envplane-telemetry create secret generic finops-kubelet-ca --from-file=ca.crt="$task_dir/kubelet.crt" --dry-run=client -o yaml | kubectl --context "$context" apply -f - >/dev/null
printf 'Candidate private TLS Secrets prepared; no key material printed.\n'
