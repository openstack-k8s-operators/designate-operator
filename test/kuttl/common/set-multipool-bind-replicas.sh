#!/bin/bash
#
# This is a base script which sets the number of bind instances of every pool
# in the multipool ConfigMap.
#
# Once a deployment is in multipool mode the pools, and therefore the number of
# bind instances per pool, are defined by the ConfigMap instead of the
# Designate CR.
#
# Usage: set-multipool-bind-replicas.sh <replicas>
set -e

REPLICAS=$1
if [ -z "$REPLICAS" ]; then
    echo "Usage: $0 <replicas>"
    exit 1
fi

pools=$(oc get -n $NAMESPACE configmap designate-multipool-config -o jsonpath='{.data.pools}')
if [ -z "$pools" ]; then
    echo "multipool ConfigMap designate-multipool-config not found or empty"
    exit 1
fi

updated=$(echo "$pools" | yq eval ".[].bindReplicas = $REPLICAS" -)
if [ -z "$updated" ] || [ "$updated" = "null" ]; then
    echo "Failed to update bindReplicas in the multipool ConfigMap"
    exit 1
fi

{
    echo 'apiVersion: v1'
    echo 'kind: ConfigMap'
    echo 'metadata:'
    echo '  name: designate-multipool-config'
    echo 'data:'
    echo '  pools: |'
    echo "$updated" | sed 's/^/    /'
} | oc apply -n $NAMESPACE -f -
