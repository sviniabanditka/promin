#!/bin/sh
# Host firewall for the k3s control-plane ports.
#
# The node runs with INPUT policy ACCEPT and no other firewall, so the API
# server (6443), the kubelet (10250) and node_exporter (9100) were reachable
# from the whole internet and were being scanned (28 distinct IPs in one day).
# Only loopback, the node itself, pods (10.42/16) and cluster IPs (10.43/16)
# may reach them; everything else is dropped. 22, 80, 443 and 8444 are not
# touched. kubectl on the host uses 127.0.0.1:6443 and keeps working; CI
# deploys through ssh + kubectl on the host and keeps working.
#
# Idempotent: re-running rebuilds the PROMIN-GUARD chain and re-inserts the
# jump at position 1 of INPUT, ahead of the kube-router / kube-proxy chains.
set -e
NODE_IP=${NODE_IP:-109.199.115.31}
PORTS=6443,10250,9100
for ipt in iptables ip6tables; do
  $ipt -N PROMIN-GUARD 2>/dev/null || $ipt -F PROMIN-GUARD
  if [ "$ipt" = iptables ]; then
    for src in 127.0.0.0/8 10.42.0.0/16 10.43.0.0/16 "$NODE_IP/32"; do
      $ipt -A PROMIN-GUARD -p tcp -m multiport --dports $PORTS -s "$src" -j ACCEPT
    done
  else
    $ipt -A PROMIN-GUARD -p tcp -m multiport --dports $PORTS -s ::1/128 -j ACCEPT
  fi
  $ipt -A PROMIN-GUARD -p tcp -m multiport --dports $PORTS -j DROP
  $ipt -D INPUT -j PROMIN-GUARD 2>/dev/null || true
  $ipt -I INPUT 1 -j PROMIN-GUARD
done
