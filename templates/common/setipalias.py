#!/usr/bin/python3
import os
import sys
import ipaddress
import netifaces
from pyroute2 import IPRoute

mapping_prefix =  os.environ.get("MAP_PREFIX")
if not len(mapping_prefix):
    print(f"{sys.argv[0]} requires MAP_PREFIX to be set in environment variables", file=sys.stderr)
    sys.exit(1)

print(f"Using {mapping_prefix} as the map prefix", file=sys.stderr)

nodefile = ""
podname = os.environ.get("POD_NAME").strip()
if not len(podname):
    print(f"{sys.argv[0]} requires POD_NAME in environment variables", file=sys.stderr)
    sys.exit(1)

print(f"Pod name is {podname}", file=sys.stderr)
namepieces = podname.split('-')
pod_index = namepieces[-1]
nodefile = f"{mapping_prefix}{pod_index}"

interface_name = os.environ.get("NAD_NAME", "designate").strip()

print(f"working with address file {nodefile}", file=sys.stderr)
filename = os.path.join('/var/lib/predictableips', nodefile)
if not os.path.exists(filename):
    print(f"Required alias address file {filename} does not exist", file=sys.stderr)
    sys.exit(1)

ip = IPRoute()
designateinterface = ip.link_lookup(ifname=interface_name)

if not len(designateinterface):
    print(f"{interface_name} attachment not present", file=sys.stderr)
    sys.exit(1)


ipfile = open(filename, "r")
ipaddr = ipfile.read()
ipfile.close()

if ipaddr:
    print(f"Setting {ipaddr} on {interface_name}", file=sys.stderr)
    # output the ipaddr to stdout so that the container-scripts/setipalias.sh can read it
    print(f"{ipaddr}")
    # Get our current addresses so we can avoid trying to set the
    # same address again.
    version = ipaddress.ip_address(ipaddr).version
    ifaceinfo = netifaces.ifaddresses(interface_name)[
        netifaces.AF_INET if version == 4 else netifaces.AF_INET6]
    current_addresses = [x['addr'] for x in ifaceinfo]
    if ipaddr not in current_addresses:
        mask_value = 32
        if version == 6:
            mask_value = 128
        ip.addr('add', index = designateinterface[0], address=ipaddr, mask=mask_value)
else:
    print('No IP address found', file=sys.stderr)

ip.close()
