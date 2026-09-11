#!/bin/sh
# Run from the laptop: copies promin-guard.sh + its systemd unit to the k3s
# node, enables the unit, then checks from outside that 6443 is closed and
# 443 still answers, and from inside the cluster that pods still reach the
# API server, the kubelet and node_exporter.
set -eu
cd "$(dirname "$0")"

KEY=${KEY:-$HOME/.ssh/tracker_ci}
HOST=${HOST:-root@ssh.sviniabanditka.com}
NODE_IP=${NODE_IP:-109.199.115.31}
SSH="ssh -i $KEY -o ConnectTimeout=15 $HOST"

echo "== copying files"
scp -q -i "$KEY" promin-guard.sh promin-guard.service "$HOST:/root/"

echo "== installing and enabling"
$SSH '
  set -e
  install -m 0755 /root/promin-guard.sh /usr/local/sbin/promin-guard.sh
  install -m 0644 /root/promin-guard.service /etc/systemd/system/promin-guard.service
  systemctl daemon-reload
  systemctl enable --now promin-guard.service
  echo "--- unit: $(systemctl is-active promin-guard.service)"
  echo "--- IPv4 chain"; iptables -S PROMIN-GUARD
  echo "--- IPv6 chain"; ip6tables -S PROMIN-GUARD
  echo "--- INPUT jumps first: $(iptables -S INPUT | sed -n 2p) / $(ip6tables -S INPUT | sed -n 2p)"
'

echo "== in-cluster access (must still work)"
$SSH '
  export KUBECONFIG=/etc/rancher/k3s/k3s.yaml
  printf "kubectl on host  : "; kubectl get --raw /readyz; echo
  printf "pod -> API       : "; kubectl -n promin exec deploy/promin -- sh -c "wget -qO- --no-check-certificate --timeout=5 https://10.43.0.1:443/version 2>/dev/null | head -c 60 || echo FAIL"; echo
  printf "pod -> kubelet   : "; kubectl -n promin exec deploy/promin -- sh -c "wget -S -qO- --no-check-certificate --timeout=5 https://'"$NODE_IP"':10250/healthz 2>&1 | head -1 || true"
  printf "pod -> exporter  : "; kubectl -n promin exec deploy/promin -- sh -c "wget -qO- --timeout=5 http://'"$NODE_IP"':9100/metrics 2>/dev/null | head -c 40 || echo FAIL"; echo
'

echo "== from outside (6443/10250/9100 must be closed, 443 open)"
for port in 6443 10250 9100; do
  if nc -zw3 "$NODE_IP" "$port" 2>/dev/null; then echo "port $port: STILL OPEN"; else echo "port $port: closed"; fi
done
if nc -zw3 "$NODE_IP" 443 2>/dev/null; then echo "port 443: open"; else echo "port 443: CLOSED — check the site!"; fi
