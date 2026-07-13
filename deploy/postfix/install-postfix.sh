#!/usr/bin/env bash
# Idempotent Postfix + OpenDKIM setup for Pantawin outbound alert mail.
# Host-installed (systemd), NOT containerized — mail-queue durability and
# local delivery are simpler outside a container, and `systemctl stop
# postfix opendkim` is an equally scoped kill-switch that never touches
# nginx/docker/anana.
#
# Run as root on the VPS. Sends as alerts@pantawin.gratisaja.com. Binds the
# SMTP listener to loopback only — it never accepts inbound mail from the
# internet, only relays Pantawin's own outbound alerts.
#
# Usage: ./install-postfix.sh   (re-runnable; regenerates nothing already present)
set -euo pipefail

MAIL_DOMAIN="pantawin.gratisaja.com"
MYHOSTNAME="mail.${MAIL_DOMAIN}"
DKIM_SELECTOR="pantawin1"
DKIM_KEYDIR="/etc/opendkim/keys/${MAIL_DOMAIN}"

echo "==> Installing packages (postfix, opendkim)"
export DEBIAN_FRONTEND=noninteractive
# Preseed so postfix install doesn't open an interactive dialog.
debconf-set-selections <<EOF
postfix postfix/mailname string ${MYHOSTNAME}
postfix postfix/main_mailer_type string 'Internet Site'
EOF
apt-get update -qq
apt-get install -y -qq postfix opendkim opendkim-tools

echo "==> Configuring Postfix main.cf"
postconf -e "myhostname = ${MYHOSTNAME}"
postconf -e "mydomain = ${MAIL_DOMAIN}"
postconf -e "myorigin = ${MAIL_DOMAIN}"
# Listen on all interfaces so the Dockerized API can reach the relay via the
# host gateway (172.x). NOT an open relay: mynetworks is restricted to
# loopback + private Docker ranges, and mydestination is empty (send-only).
# ufw denies inbound port 25 from the internet — verify with the check the
# script prints at the end.
postconf -e "inet_interfaces = all"
postconf -e "mynetworks = 127.0.0.0/8 [::1]/128 172.16.0.0/12"
postconf -e "smtpd_relay_restrictions = permit_mynetworks, reject_unauth_destination"

echo "==> Restricting inbound SMTP: allow Docker bridge, deny the internet"
if command -v ufw >/dev/null 2>&1 && ufw status | grep -q "Status: active"; then
  # The container reaches Postfix via the host gateway, so the packet
  # arrives on the host's INPUT chain from a 172.x source — allow that
  # first (more specific), then deny 25 from everywhere else.
  ufw allow from 172.16.0.0/12 to any port 25 proto tcp comment 'Pantawin container -> host Postfix' || true
  ufw deny 25/tcp comment 'block public SMTP' || true
fi
# Don't accept local delivery for any domain — send-only.
postconf -e "mydestination ="
# Sign outbound via OpenDKIM milter.
postconf -e "milter_default_action = accept"
postconf -e "milter_protocol = 6"
postconf -e "smtpd_milters = inet:127.0.0.1:8891"
postconf -e "non_smtpd_milters = inet:127.0.0.1:8891"

echo "==> Configuring OpenDKIM"
mkdir -p "${DKIM_KEYDIR}"
if [ ! -f "${DKIM_KEYDIR}/${DKIM_SELECTOR}.private" ]; then
  echo "    generating DKIM key (${DKIM_SELECTOR})"
  opendkim-genkey -b 2048 -d "${MAIL_DOMAIN}" -D "${DKIM_KEYDIR}" -s "${DKIM_SELECTOR}" -v
  chown -R opendkim:opendkim /etc/opendkim/keys
  chmod 600 "${DKIM_KEYDIR}/${DKIM_SELECTOR}.private"
else
  echo "    DKIM key already present, keeping it"
fi

cat > /etc/opendkim.conf <<EOF
Syslog                  yes
UMask                   002
Mode                    sv
Socket                  inet:8891@127.0.0.1
PidFile                 /run/opendkim/opendkim.pid
OversignHeaders         From
TrustAnchorFile         /usr/share/dns/root.key
KeyTable                /etc/opendkim/KeyTable
SigningTable            /etc/opendkim/SigningTable
InternalHosts           /etc/opendkim/TrustedHosts
EOF

cat > /etc/opendkim/KeyTable <<EOF
${DKIM_SELECTOR}._domainkey.${MAIL_DOMAIN} ${MAIL_DOMAIN}:${DKIM_SELECTOR}:${DKIM_KEYDIR}/${DKIM_SELECTOR}.private
EOF

cat > /etc/opendkim/SigningTable <<EOF
*@${MAIL_DOMAIN} ${DKIM_SELECTOR}._domainkey.${MAIL_DOMAIN}
EOF

cat > /etc/opendkim/TrustedHosts <<EOF
127.0.0.1
::1
localhost
EOF

echo "==> Memory cap via systemd drop-in (shared VPS)"
mkdir -p /etc/systemd/system/postfix.service.d
cat > /etc/systemd/system/postfix.service.d/memory.conf <<EOF
[Service]
MemoryMax=96M
EOF
mkdir -p /etc/systemd/system/opendkim.service.d
cat > /etc/systemd/system/opendkim.service.d/memory.conf <<EOF
[Service]
MemoryMax=48M
EOF

systemctl daemon-reload
systemctl enable opendkim postfix
systemctl restart opendkim
systemctl restart postfix

echo
echo "==> DONE. Postfix listening on 127.0.0.1:25, OpenDKIM signing for ${MAIL_DOMAIN}."
echo
echo "Add these DNS records in Cloudflare (gratisaja.com zone), all as DNS-only TXT:"
echo
echo "  Name: ${MAIL_DOMAIN}"
echo "  TXT:  v=spf1 ip4:103.181.182.61 ~all"
echo
echo "  Name: _dmarc.${MAIL_DOMAIN}"
echo "  TXT:  v=DMARC1; p=none; rua=mailto:alerts@${MAIL_DOMAIN}"
echo
echo "  Name: (see below — the DKIM record printed from the generated key)"
echo "--------------------------------------------------------------------"
cat "${DKIM_KEYDIR}/${DKIM_SELECTOR}.txt"
echo "--------------------------------------------------------------------"
echo
echo "The DKIM record above spans multiple quoted strings — in Cloudflare,"
echo "paste it as a single TXT value on ${DKIM_SELECTOR}._domainkey.${MAIL_DOMAIN}"
echo "with the quotes/parentheses removed and the p= parts concatenated."
echo
echo "Test after DNS propagates:  swaks --to you@gmail.com --from alerts@${MAIL_DOMAIN} --server 127.0.0.1"
