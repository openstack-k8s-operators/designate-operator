#!/bin/bash
#
# This is a base script which will verify the pools.yaml generated file.
#
# Get the ConfigMap content
NUM_OF_SERVICES=$1
EXPECTED_IPS_NUM=$((NUM_OF_SERVICES * 2))
config_content=$(oc get -n $NAMESPACE configmap designate-pools-yaml-config-map -o jsonpath='{.data.pools-yaml-content}')

# Validate pools.yaml config map YAML structure
if ! yq eval '.' <<< "$config_content" &> /dev/null; then
    echo "Invalid YAML structure"
    exit 1
fi

# Assert pool's name
if [ "$(echo "$config_content" | yq eval '.[0].name' -)" != "default" ]; then
    echo "Pool name is not 'default'"
    exit 1
fi

# Assert pool's NS records
if [ "$(echo "$config_content" | yq eval '.[0].ns_records[0].hostname' -)" != "ns1.example.com." ]; then
    echo "First NS record hostname is incorrect"
    exit 1
fi
if [ "$(echo "$config_content" | yq eval '.[0].ns_records[1].hostname' -)" != "ns2.example.com." ]; then
    echo "Second NS record hostname is incorrect"
    exit 1
fi

# Check nameserver IPs
nameserver_ips=$(echo "$config_content" | yq eval '.[0].nameservers[].host' -)
for ip in $nameserver_ips; do
    if [[ ! $ip =~ ^172\.28\.0\.[0-9]+$ ]]; then
        echo "Invalid nameserver IP format: $ip"
        exit 1
    fi
done

# Check master IPs
master_ips=$(echo "$config_content" | yq eval '.[0].targets[].masters[].host' -)
for ip in $master_ips; do
    if [[ ! $ip =~ ^172\.28\.0\.[0-9]+$ ]]; then
        echo "Invalid master IP format: $ip"
        exit 1
    fi
done

# Check target.option IPs
target_ips=$(echo "$config_content" | yq eval '.[0].targets[].options.host' -)
rndc_ips=$(echo "$config_content" | yq eval '.[0].targets[].options.rndc_host' -)
for ip in $target_ips $rndc_ips; do
    if [[ ! $ip =~ ^172\.28\.0\.[0-9]+$ ]]; then
        echo "Invalid target/rndc IP format: $ip"
        exit 1
    fi
done

# Count total unique IPs
all_ips=$(echo "$config_content" | yq eval '
    .[0].nameservers[].host,
    .[0].targets[].masters[].host,
    .[0].targets[].options.host,
    .[0].targets[].options.rndc_host' - | sort -u)
unique_ip_count=$(echo "$all_ips" | wc -l)
if [ "$unique_ip_count" -ne $EXPECTED_IPS_NUM ]; then
    echo "Expected $EXPECTED_IPS_NUM unique IPs, found $unique_ip_count"
    exit 1
fi

# Verify port numbers
nameserver_ports=$(echo "$config_content" | yq eval '.[0].nameservers[].port' -)
for port in $nameserver_ports; do
    if [ "$port" -ne 53 ]; then
        echo "Invalid nameserver port: $port"
        exit 1
    fi
done
master_ports=$(echo "$config_content" | yq eval '.[0].targets[].masters[].port' -)
for port in $master_ports; do
    if [ "$port" -ne 5354 ]; then
        echo "Invalid master port: $port"
        exit 1
    fi
done
rndc_ports=$(echo "$config_content" | yq eval '.[0].targets[].options.rndc_port' -)
for port in $rndc_ports; do
    if [ "$port" -ne 953 ]; then
        echo "Invalid rndc port: $port"
        exit 1
    fi
done
echo "pools.yaml generated file was verified successfully"
