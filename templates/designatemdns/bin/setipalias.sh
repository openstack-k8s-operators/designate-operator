#!/bin/bash
#
# Copyright 2024 Red Hat Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License"); you may
# not use this file except in compliance with the License. You may obtain
# a copy of the License at
#
#      http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS, WITHOUT
# WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the
# License for the specific language governing permissions and limitations
# under the License.
set -ex

SVC_CFG_MERGED=/var/lib/config-data/merged/designate.conf

# format_listen_addr addr port
#   Returns a host:port string suitable for Designate's listen config.
#   Bare IPv6 addresses are wrapped in brackets (e.g. fd00::1 -> [fd00::1]:5354)
#   to comply with RFC 3986. Already-bracketed addresses and IPv4 addresses are
#   passed through unchanged.
format_listen_addr() {
    local addr=$1
    local port=$2
    if [[ "$addr" == \[*\] ]]; then
        echo "${addr}:${port}"
    elif [[ "$addr" == *:*:* ]]; then
        echo "[${addr}]:${port}"
    else
        echo "${addr}:${port}"
    fi
}

IPADDR=$(/usr/local/bin/container-scripts/setipalias.py) || true
LISTEN_VALUE=""
SEPARATOR=""
if [ -n "$IPADDR" ]; then
    LISTEN_VALUE=$(format_listen_addr "$IPADDR" 5354)
    SEPARATOR=","
else
    echo "No predictable IP found"
fi

POD_IP=$(grep "$HOSTNAME" /etc/hosts | awk '{print $1}' | head -1)
if [ -n "$POD_IP" ]; then
    LISTEN_VALUE="${LISTEN_VALUE}${SEPARATOR}$(format_listen_addr "$POD_IP" 5354)"
else
    echo "No POD_IP found"
fi

if [ -n "$LISTEN_VALUE" ]; then
    echo "Setting listen value to ${LISTEN_VALUE}"
    crudini --set "$SVC_CFG_MERGED" 'service:mdns' 'listen' "${LISTEN_VALUE}"
else
    echo "No value"
fi
