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

MERGEPATH=/var/lib/config-data/merged

# Unlike  merge_dir that used crudini, this assumes that different configs
# are layered in tiers by using distinct filenames and oslo.config's configdir
# functionality to "build up" the complete configuration.
function copy_config_dir {
    echo copying config dir $1
    for conf in $(find $1 -type f); do
        conf_base=$(basename $conf)
        # Redirect the configdir files to designate.conf.d otherwise
        # put in the root of the merged volume.
        if [[ ${conf_base} =~ [0-9]{2}.config.conf ]]; then
            echo copy ${conf} to ${MERGEPATH}/designate.conf.d
            cp -f ${conf} ${MERGEPATH}/designate.conf.d/
            chmod 0660 ${MERGEPATH}/designate.conf.d/${conf_base}
        else
            cp -f ${conf} ${MERGEPATH}/
        fi
    done
}

# Clear existing targets in case we are looping.
rm -rf ${MERGEPATH}/* /var/lib/config-data/config-overwrites/*

# Pickup packaged config. This allows us to setup a merged config that is
# roughly a mirror of what we want it to look like if it is mounted at
# /etc/designate in the pod. This is important for things like the API service
# that has several additional httpd/webservice related files that are packaged.
# Note that this depends on init container image being the service container
# image for the main service container in the pod.
# NOTE: files that appear in the configuration secrets can overwrite these
# files copied here.
for f in $(find /etc/designate -maxdepth 1 -type f); do
    target=$(basename $f)
    cp -f ${f} ${MERGEPATH}/${target}
    chmod 0660 ${MERGEPATH}/${target}
done

mkdir ${MERGEPATH}/designate.conf.d
chmod 0775 ${MERGEPATH}/designate.conf.d

# copy the main controller configs over
copy_config_dir /var/lib/config-data/default

# copy the service (sub-resource) controller configs over if present
# (may be missing if this is a non-service pod like db-create job etc.
if test -d /var/lib/config-data/service; then
    copy_config_dir /var/lib/config-data/service
fi

OVERWRITE_DEST=/var/lib/config-data/config-overwrites
if test -d ${OVERWRITE_DEST}; then
    if test -d /var/lib/config-data/common-overwrites; then
        cp -r /var/lib/config-data/common-overwrites ${OVERWRITE_DEST}
    fi
    if test -d /var/lib/config-data/overwrites; then
        cp -r /var/lib/config-data/overwrites ${OVERWRITE_DEST}
    fi
fi
