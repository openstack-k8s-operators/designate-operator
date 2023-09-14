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
export PASSWORD=${AdminPassword:?"Please specify a AdminPassword variable."}
export DBHOST=${DatabaseHost:?"Please specify a DatabaseHost variable."}
export DBUSER=${DatabaseUser:?"Please specify a DatabaseUser variable."}
export DBPASSWORD=${DatabasePassword:?"Please specify a DatabasePassword variable."}
export DB=${DatabaseName:-"designate"}
export TRANSPORTURL=${TransportURL:-""}
export BACKENDURL=${BackendURL:-"redis://redis:6379/"}
export BACKENDTYPE=${BackendType:-"None"}

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

# set secrets in the config-data
crudini --set ${SVC_CFG_MERGED} database connection mysql+pymysql://${DBUSER}:${DBPASSWORD}@${DBHOST}/${DB}
crudini --set ${SVC_CFG_MERGED} storage:sqlalchemy connection mysql+pymysql://root:${DBPASSWORD}@${DBHOST}/${DB}?charset=utf8
crudini --set ${SVC_CFG_MERGED} keystone_authtoken password $PASSWORD
if [ -n "$TRANSPORTURL" ]; then
    crudini --set ${SVC_CFG_MERGED} DEFAULT transport_url $TRANSPORTURL
fi
if [ -n "$BACKENDURL" ]; then
    crudini --set ${SVC_CFG_MERGED} coordination backend_url $BACKENDURL
fi

# Determine what the backend we are using and initialize accordingly.
if [ "$BACKENDTYPE" == "bind9" ]; then
    msgout "INFO" "bind9 setup"
    # setup some critical env vars
    BIND_SERVICE_NAME=named
    BIND_CFG_DIR=/etc/named
    BIND_CFG_FILE=/etc/named/named.conf
    BIND_VAR_DIR=/var/named
    BIND_CACHE_DIR=/var/cache/named
    BIND_USER=named
    BIND_GROUP=named
    BIND_PORT=53
    RNDC_PORT=953
    MYIPADDR=$(hostname --ip-address)

    setup_bind9

    # change the tags to values
    sudo sed -i 's/IPV4ADDR/'$MYIPADDR'/g' $BIND_CFG_FILE
    sudo sed -i 's/BINDPORT/'$BIND_PORT'/g' $BIND_CFG_FILE
    sudo sed -i 's/RNDCPORT/'$RNDC_PORT'/g' $BIND_CFG_FILE

    sed -i 's/IPV4ADDR/'$MYIPADDR'/g' $BIND_CFG_DIR/rndc.conf
    sed -i 's/RNDCPORT/'$RNDC_PORT'/g' $BIND_CFG_DIR/rndc.conf


elif [ "$BACKENDTYPE" = "unbound" ]; then
    msgout "INFO" "unbound setup ****UNDER CONSTRUCTION***"
elif [ "$BACKENDTYPE" = "byo" ]; then
    msgout "INFO" "BYO server setup ****UNDER CONSTRUCTION***"
fi

# NOTE:dkehn - REMOVED because Kolla_set & start copy eveyrthing.
# I'm doing this to get the designate.conf w/all the tags with values.
cp -a ${SVC_CFG_MERGED} ${SVC_CFG}
