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

# expect that the common.sh is in the same dir as the calling script
SCRIPTPATH="$( cd "$(dirname "$0")" >/dev/null 2>&1 ; pwd -P )"
. ${SCRIPTPATH}/common.sh --source-only

# Merge all templates from config CM
for dir in /var/lib/config-data/default; do
    merge_config_dir ${dir}
done

mkdir /var/lib/config-data/merged/named
cp -f /var/lib/config-data/default/named/* /var/lib/config-data/merged/named/

# Add TSIG configuration if it exists (for multipool non-default pools)
if [[ -f "/var/lib/tsig/tsigkeys.conf" ]]; then
    cp /var/lib/tsig/tsigkeys.conf /var/lib/config-data/merged/named/tsigkeys.conf
    echo 'include "/etc/named/tsigkeys.conf";' >> /var/lib/config-data/merged/named.conf
    echo "Added TSIG configuration to named.conf"
fi

# Using the index for the podname, get the matching rndc key and copy it into the proper location

if [[ -z "${POD_NAME}" ]]; then
    echo "ERROR: requires the POD_NAME variable to be set"
    exit 1
fi

# get the index off of the pod name
set -f
name_parts=(${POD_NAME//-/ })
pod_index="${name_parts[-1]}"

# Try to read RNDC key name from per-pool ConfigMap (multipool mode)
rndc_key_map_file="/var/lib/predictableips/rndc_key_${pod_index}"
if [[ -f "${rndc_key_map_file}" ]]; then
    # Multipool mode: read the RNDC key name from ConfigMap
    rndc_key_name=$(cat "${rndc_key_map_file}")
    rndc_key_filename="/var/lib/config-data/keys/${rndc_key_name}"
    echo "Using RNDC key from ConfigMap: ${rndc_key_name}"
else
    # Single-pool mode: use pod index to determine RNDC key
    if [[ -z "${RNDC_PREFIX}" ]]; then
        rndc_prefix="rndc-key-"
    else
        rndc_prefix="${RNDC_PREFIX}-"
    fi
    rndc_key_filename="/var/lib/config-data/keys/${rndc_prefix}${pod_index}"
    echo "Using RNDC key from pod index: ${rndc_prefix}${pod_index}"
fi

if [[ -f "${rndc_key_filename}" ]]; then
    cp ${rndc_key_filename} /var/lib/config-data/merged/named/rndc.key
else
    echo "ERROR: rndc key not found at ${rndc_key_filename}!"
    exit 1
fi
