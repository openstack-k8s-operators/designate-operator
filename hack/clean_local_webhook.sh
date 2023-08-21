#!/bin/bash
set -ex

oc delete validatingwebhookconfiguration/vdesignate.kb.io --ignore-not-found
oc delete mutatingwebhookconfiguration/mdesignate.kb.io --ignore-not-found
