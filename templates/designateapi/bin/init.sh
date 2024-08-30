#!/bin//bash
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

# This script generates the designate.conf/logging.conf file and
# copies the result to the ephemeral /var/lib/config-data/merged volume.
#
# Secrets are obtained from ENV variables.
export PASSWORD=${AdminPassword:?"Please specify a AdminPassword variable."}
export TRANSPORTURL=${TransportURL:-""}

VERBOSE="True"

SVC_CFG=/etc/designate/designate.conf
SVC_CFG_MERGED=/var/lib/config-data/merged/designate.conf

# expect that the common.sh is in the same dir as the calling script
SCRIPTPATH="$( cd "$(dirname "$0")" >/dev/null 2>&1 ; pwd -P )"
. ${SCRIPTPATH}/common.sh --source-only

# Copy default service config from container image as base
cp -a ${SVC_CFG} ${SVC_CFG_MERGED}

# Merge all templates from config CM
for dir in /var/lib/config-data/default; do
    merge_config_dir ${dir}
done

# set secrets in the config-data
crudini --set ${SVC_CFG_MERGED} keystone_authtoken password $PASSWORD
if [ -n "$TRANSPORTURL" ]; then
    crudini --set ${SVC_CFG_MERGED} DEFAULT transport_url $TRANSPORTURL
fi
if [ -n "$BACKENDURL" ]; then
    crudini --set ${SVC_CFG_MERGED} coordination backend_url $BACKENDURL
fi

# NOTE:dkehn - REMOVED because Kolla_set & start copy eveyrthing.
# I'm doing this to get the designate.conf w/all the tags with values.
cp -a ${SVC_CFG_MERGED} ${SVC_CFG}
