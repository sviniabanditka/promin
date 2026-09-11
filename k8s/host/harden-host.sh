#!/bin/sh
# Run from the laptop. Two host-level audit findings that no application code
# can fix:
#  1. A forgotten TorrServer (/opt/torrserver, root, enabled) with its web UI
#     open to the internet on :8123 without auth.
#  2. sshd: /etc/ssh/sshd_config.d/50-cloud-init.conf re-enables
#     PasswordAuthentication for root; ~7k failed password logins a day.
# Everything here keeps the key-based root login CI and the operator use.
set -eu
KEY=${KEY:-$HOME/.ssh/tracker_ci}
HOST=${HOST:-root@ssh.sviniabanditka.com}
SSH="ssh -i $KEY -o ConnectTimeout=15 $HOST"

echo "== TorrServer"
$SSH '
  if systemctl list-unit-files torrserver.service >/dev/null 2>&1; then
    systemctl disable --now torrserver.service || true
    echo "torrserver: $(systemctl is-active torrserver.service || true) / $(systemctl is-enabled torrserver.service 2>/dev/null || true)"
  fi
  if [ -d /opt/torrserver ]; then mv /opt/torrserver /root/torrserver.disabled.$(date +%Y%m%d) && echo "moved /opt/torrserver aside"; fi
  ss -tln | grep -E ":(8123|34363) " || echo "8123/34363: nothing listening"
'

echo "== sshd: keys only"
$SSH '
  printf "PasswordAuthentication no\nPermitRootLogin prohibit-password\nKbdInteractiveAuthentication no\nMaxAuthTries 4\n" > /etc/ssh/sshd_config.d/00-hardening.conf
  sshd -t && systemctl reload ssh
  sshd -T | grep -Ei "^(passwordauthentication|permitrootlogin|kbdinteractiveauthentication|maxauthtries) "
'

echo "== key login still works (second connection)"
$SSH 'echo ok: $(hostname)'
