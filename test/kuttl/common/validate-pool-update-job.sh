#!/bin/bash
#
# Verifies the designate-manage pool update Job completed successfully.
# Job names are designate-pool-update-<unix>. With preserveJobs false, finished Jobs
# get a TTL and may disappear; then we require status.hash.pool-update on the Designate
# CR (set only after a successful Job). When multiple Jobs exist, the newest by
# creationTimestamp must have succeeded so scaling does not pass on a stale Job.

set -euo pipefail

jobs_json=$(oc get -n "$NAMESPACE" jobs -o json)
pool_jobs=$(echo "$jobs_json" | jq '[.items[] | select(.metadata.name | startswith("designate-pool-update-"))]')
count=$(echo "$pool_jobs" | jq 'length')

if [ "$count" -eq 0 ]; then
    pool_hash=$(oc get -n "$NAMESPACE" designate designate -o jsonpath='{.status.hash.pool-update}' 2>/dev/null || true)
    if [ -n "$pool_hash" ]; then
        echo "No pool-update Job in namespace (likely TTL); status.hash.pool-update is set on Designate CR"
        exit 0
    fi
    echo "No designate-pool-update-* Job found and status.hash.pool-update is empty"
    exit 1
fi

latest=$(echo "$pool_jobs" | jq 'sort_by(.metadata.creationTimestamp) | last')
name=$(echo "$latest" | jq -r '.metadata.name')
succeeded=$(echo "$latest" | jq -r '.status.succeeded // 0')
failed=$(echo "$latest" | jq -r '.status.failed // 0')
active=$(echo "$latest" | jq -r '.status.active // 0')

if [ "$succeeded" -ge 1 ]; then
    echo "Pool update Job completed successfully: $name"
    exit 0
fi

if [ "$failed" -ge 1 ]; then
    echo "Pool update Job $name has failures (status.failed=$failed)"
    exit 1
fi

if [ "$active" -ge 1 ]; then
    echo "Pool update Job $name still running (active=$active)"
    exit 1
fi

echo "Pool update Job $name not complete yet (succeeded=$succeeded active=$active)"
exit 1
