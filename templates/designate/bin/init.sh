#!/bin//bash
#
# Copyright 2020 Red Hat Inc.
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
VERBOSE="True"

SVC_CFG=/etc/designate/designate.conf
SVC_CFG_MERGED=/var/lib/config-data/merged/designate.conf

# expect that the common.sh is in the same dir as the calling script
SCRIPTPATH="$( cd "$(dirname "$0")" >/dev/null 2>&1 ; pwd -P )"
. ${SCRIPTPATH}/common.sh --source-only

# this function writes a message to the console in the format:
# YYYY-MM-DD HH:MM:SS: module [thead_id]: <level> <str>
# called: msgout "INFO" "message string"
function msgout {
    local xtrace
    xtrace=$(set +o | grep xtrace)
    set +o xtrace

    local level=$1
    local str=$2
    local tm
    tm=`date +"%Y-%m-%d %H:%M:%S"`
    if [ "$level" == "DEBUG" ] && [ -z "$VERBOSE" ]; then
        $xtrace
        return 0
    else
        echo "$tm: $PROG [$$]: $1: $str"
    fi
    $xtrace
}
# Sets up the bind9
function setup_bind9 {
    msgout "INFO" "setup_bind9 Called"
    if ! getent group $BIND_GROUP >/dev/null; then
        sudo groupadd $BIND_GROUP
    fi

    if [[ ! -d $BIND_CFG_DIR ]]; then
        sudo mkdir -p $BIND_CFG_DIR
        sudo chown $BIND_USER:$BIND_GROUP $BIND_CFG_DIR
    fi

    if [[ ! -d $BIND_VAR_DIR ]]; then
        sudo mkdir -p $BIND_VAR_DIR
        sudo chown $BIND_USER:$BIND_GROUP $BIND_VAR_DIR
    fi

    if [[ ! -d $BIND_CACHE_DIR ]]; then
        sudo mkdir -p $BIND_CACHE_DIR
        sudo chown $BIND_USER:$BIND_GROUP $BIND_CACHE_DIR
    fi

    sudo chmod -R g+r $BIND_CFG_DIR
    sudo chmod -R g+rw $BIND_VAR_DIR
    sudo chmod -R g+rw $BIND_CACHE_DIR

}

# Copy default service config from container image as base
# cp -a ${SVC_CFG} ${SVC_CFG_MERGED}

# Merge all templates from config CM
for dir in /var/lib/config-data/default; do
    merge_config_dir ${dir}
done

# NOTE:dkehn - REMOVED because Kolla_set & start copy eveyrthing.
# I'm doing this to get the designate.conf w/all the tags with values.
cp -a ${SVC_CFG_MERGED} ${SVC_CFG}
