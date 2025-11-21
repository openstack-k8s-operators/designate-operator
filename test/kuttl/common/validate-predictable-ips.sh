#!/bin/bash
#
# This is a base script which will verify Bind9 & Mdns predictable IPs.
#
# Check Bind9 predictable IPs configmap
NUM_OF_SERVICES=$1
bind_ips=$(oc get -n $NAMESPACE configmap designate-bind-ip-map -o json | jq -r '.data | values[]')
if [ $(echo "$bind_ips" | wc -l) -ne ${NUM_OF_SERVICES} ]; then
    echo "Expected ${NUM_OF_SERVICES} bind addresses, found $(echo "$bind_ips" | wc -l)"
    exit 1
fi
for ip in $bind_ips; do
    if [[ ! $ip =~ ^172\.28\.0\.[0-9]+$ ]]; then
        echo "Invalid bind IP format: $ip"
        exit 1
    fi
done

# Check Mdns predictable IPs configmap
mdns_ips=$(oc get -n $NAMESPACE configmap designate-mdns-ip-map -o json | jq -r '.data | values[]')
if [ $(echo "$mdns_ips" | wc -l) -ne ${NUM_OF_SERVICES} ]; then
    echo "Expected ${NUM_OF_SERVICES} mdns addresses, found $(echo "$mdns_ips" | wc -l)"
    exit 1
fi
for ip in $mdns_ips; do
    if [[ ! $ip =~ ^172\.28\.0\.[0-9]+$ ]]; then
        echo "Invalid mdns IP format: $ip"
        exit 1
    fi
done
echo "Bind9 & Mdns predictable IPs were verified successfully"
