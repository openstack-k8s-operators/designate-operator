# Deploying Designate in an Openstack On OpenShift cluster

This document describes how to enable the Designate DNSaaS in a deployment
managed by the OpenStack on OpenShift operator (i.e. openstack-operator).
Simply enabling designate in the OpenStack control plane CR will not be
sufficient as there are depenedencies and a usable deployment will need some
additional configuration that must be configured by the administrator.

A standard OpenStack deployment configures most of what is needed but there are
additional optional services that need to be enabled. Designate also requires
some additional networks to be defined to create the internal designate
management network and the connectivity for the DNS related services: the BIND
nameserver and the unbound DNS resolver.

> The designate-operator is currently under active development. At the time of
> writing it has reached a dev/tech-preview state where it is possible to
> deploy a functional designate for proof-of-concept or experimentation. Where
> documented, sections that are likely to changed are identified as *For FR2
> Only*.
>

## Enable the Redis instance

Designate uses Redis to coordinate tasks. To enable it, edit the control plane
to enable redis and add the `designate-redis` instance to the templates
parameter, e.g.:

```yaml
spec:
  .
  .
  .
  redis:
    enabled: true
    templates:
      designate-redis:
        replicas: 3
        tls: {}
```

Confirm that the instance is running before continuing.
```sh
oc wait --for=condition=ready --timeout=10s pod/designate-redis-redis-0
pod/designate-redis-redis-0 condition met
```

*It is not necessary to remove any other Redis instances that are already defined.*

## Create the Designate Networks

Designate manages the DNS records stored in one or more BIND servers. This
occurs using standard DNS protocols and *rndc* commands over a secure, dedicated
network link. This requires changes to the *NodeNetworkConnectionPolicy* for the
worker nodes and a *NetworkAttachmentDefinition* for connecting pods to the
network. Keep the following details in mind when choosing IP ranges, VLAN ids,
etc.:

* Check for VLAN id conflicts.
* Ensure the VLAN ids chosen are not blocked by switch configuration.
* Check for IP conflicts. The IP address will need to be unique for each worker
  node.
* Find the base interface for each worker node. Change the base interface if
  necessary *(while it is common that the base interface will be consistent
  across workers in a cluster, it is possible that there may be differences*

### NodeNetworkConfigurationPolicy changes

> The following instructions assume a base interface of enp7s0. Please examine
> your system configuration to determine the correct base interface. For the
> *designate* network interface, this will typically be the same as the base
> interface for the *internalapi*.  For *designateext*, this may be the same as
> the *designate* network, or a dedicated network interface that might be being
> used for routing external networks to and from the cluster (e.g. a *public*
> network)

To create these networks, add the following to the *interfaces* list under the
*desiredState* for the *NodeNetworkConfigurationPolicy* for each node. This can
be done by running `oc edit -n openstack nncp <node name>` and, paying close
attention to the indentation, inserting the definitions.  Alternatively, edit
the source yaml file for the node's configuration policy and re-apply..

```yaml
    - description: designate vlan interface
      ipv4:
        address:
        - ip: 172.28.0.10  # Change for each worker node
          prefix-length: "24"
        dhcp: false
        enabled: true
      ipv6:
        enabled: false
      mtu: 1500
      name: designate
      state: up
      type: vlan
      vlan:
        base-iface: enp7s0
        id: "25"           # Ensure there is no conflicts with existing VLANs
    - description: designate external vlan interface
      ipv4:
        address:
        - ip: 172.50.0.10  # Change for each worker node
          prefix-length: "24"
        dhcp: false
        enabled: true
      ipv6:
        enabled: false
      mtu: 1500
      name: designateext
      state: up
      type: vlan
      vlan:
        base-iface: enp7s0
        id: "26"           # Ensure there is no conflicts with existing VLANs

```
Confirm all changes were applied by running:
```sh
> oc get -n openstack nncp
NAME         STATUS      REASON
worker-0     Available   SuccessfullyConfigured
worker-1     Available   SuccessfullyConfigured
worker-2     Available   SuccessfullyConfigured
```

### MetalLB Resources

The **designateext** network is used when defining Kubernetes Services to
access the DNS based services deployed by designate (i.e. the Unbound resolver
  and the BIND servers). MetalLB is used to provide management of dedicated
address pools so the services can enjoy a static mapping of Services to Pods.

> In some environments, the interface may be the from of `baseinterface.vlan
> id`. In other environments, it might take the name from the
> *NodeNetworkConfigurationPolicy*. If in doubt, check the interfaces on a
> worker node to see how names are being mapped.

For example, create a file named *designate_metallb_resources.yaml* that contains
the following:

```yaml
---
apiVersion: metallb.io/v1beta1
kind: IPAddressPool
metadata:
  namespace: metallb-system
  name: designateext
spec:
  autoAssign: false
  addresses:
  - 172.50.0.80-172.50.0.110
---
apiVersion: metallb.io/v1beta1
kind: L2Advertisement
metadata:
  name: designateext
  namespace: metallb-system
spec:
  ipAddressPools:
  - designateext
  interfaces:
  - designateext

```

And apply with:
```sh
oc apply -n metallb-system -f designate_metallb_resources.yaml
```
The results can be confirmed by running the following commands and verifying the results:
```sh
oc get -n metallb-system ipaddresspool designateext -o jsonpath='{.spec}' | jq
{
  "addresses": [
    "172.50.0.80-172.50.0.110"
  ],
  "autoAssign": false,
  "avoidBuggyIPs": false
}
oc get -n metallb-system l2advertisement designateext -o jsonpath='{.spec}' | jq
{
  "interfaces": [
    "designateext"
  ],
  "ipAddressPools": [
    "designateext"
  ]
}
```

### Network Attachment definitions

The *designate* network attachment definition is required to connect the DNS
backend services to the dedicated designate specific network. Note that
designate requires exclusive use of this network and it's IP allocation range.
Do not try to use this network attachment for other functions.

> In some environments, the master interface in the
> *NetworkAttachmentDefinition*  may be the from of `baseinterface.vlan id`. In
> other environments, it might take the name from the
> *NodeNetworkConfigurationPolicy*. If in doubt, check the interfaces on a
> worker node to see how names are being mapped.

For example, create a file named *designate\_nad.yaml* with the following
contents.

```yaml
---
apiVersion: k8s.cni.cncf.io/v1
kind: NetworkAttachmentDefinition
metadata:
  name: designate
  namespace: openstack
spec:
  config: |
    {
      "cniVersion": "0.3.1",
      "name": "designate",
      "type": "macvlan",
      "master": "designate",
      "ipam": {
        "type": "whereabouts",
        "range": "172.28.0.0/24",
        "range_start": "172.28.0.30",
        "range_end": "172.28.0.70"
      }
    }
---
apiVersion: k8s.cni.cncf.io/v1
kind: NetworkAttachmentDefinition
metadata:
  name: designateext
  namespace: openstack
spec:
  config: |
    {
      "cniVersion": "0.3.1",
      "name": "designateext",
      "type": "macvlan",
      "master": "designateext",
      "ipam": {
        "type": "whereabouts",
        "range": "172.50.0.0/24",
        "range_start": "172.50.0.30",
        "range_end": "172.50.0.70"
      }
    }
```

To apply run the following:

```sh
> oc apply -n openstack -f designate_nad.yaml
```

Confirm the network attachments were created as expected by running:
```sh
> oc get -n openstack net-attach-def | grep designate | gawk -e '{print $1}'
designate
designateext
```

## Deploying Designate

#### For FR2 branch only

The *designate\_ns\_records\_params* configmap should be created before
enabling designate. Specify *hostname* values appropriate for your environment.
For example, create a file named *ns_records.yaml* with the following contents:

```yaml
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: designate-ns-records-params
data:
  ns_records: |
    - hostname: ns1.mycloud.com.
      priority: 1
    - hostname: ns2.mycloud.com.
      priority: 2

```
To create the map, run the following:

```sh
> oc apply -n openstack -f ns_records.yaml
```

Confirm the configmap was created and contains the expected values:

```sh
> oc get cm -n openstack designate-ns-records-params
NAME                          DATA   AGE
designate-ns-records-params   1      39s
> oc get cm -n openstack designate-ns-records-params -o jsonpath='{.data.ns_records}'
- hostname: ns1.mycloud.com
  priority: 1
- hostname: ns2.mycloud.com
  priority: 2

```

## Configure Designate in the OpenStack Control Plane

The following operations all apply to the current OpenStackControlPlane
instance. The method of applying the changes is up the reader but this document
will assume the contents are applied either to the source yaml or through the
appropriate edit command with the proper indentation.

If editing the controlplane with `oc edit`, there are many empty values such as
`backendMdnsServerProtocol: ""` that can be ignored. If a parameter described
below exists but has a different value, change it to the value specified.

### designate

> *For FR2 Only* Designate has some RBAC related issues. While these will
> resolved in the future, it is recommended that RBAC policy checks be disabled.
> This is done by overriding the oslo_policy using customServiceConfig in
> several sections, including the root designate section, e.g.

```yaml
    customServiceConfig: |
      # For FR2 Only.
      [oslo_policy]
      enforce_scope = False
      enforce_new_defaults = False

```

This is tech preview only workaround. Once the RBAC issues are resolved, this
workaround should no longer be neeeded.

### designateAPI

Add the following under the designateAPI section.

```yaml
    customServiceConfig: |
      # For FR2 Only.
      [oslo_policy]
      enforce_scope = False
      enforce_new_defaults = False
    networkAttachments:
    - internalapi
    override:
      service:
        internal:
          metadata:
            annotations:
              metallb.universe.tf/address-pool: internalapi
              metallb.universe.tf/allow-shared-ip: internalapi
              metallb.universe.tf/loadBalancerIPs: 172.17.0.80
          spec:
            type: LoadBalancer
```

* The annotations should match that of other API services used in the current
OpenstackControlPlane *

### designateBackendbind9

The BIND name server uses persistent storage. This example sets the
`storageClass` to `local-storage` only for testing purposes. It would be more
appropriate to use a distributed storage class in a production system.

Also note that the networkAttachments might differ in a production system if an
additional dedicated network is used to reach the BIND servers.

Add the following under the designateBackendbind9 section:

```yaml
    controlNetworkName: designate  # FR2 Only
    databaseAccount: designate     # FR2 Only
    networkAttachments:
      - designate
    override:
      services:
        - metadata:
            annotations:
              metallb.universe.tf/address-pool: designateext
              metallb.universe.tf/allow-shared-ip: designateext
              metallb.universe.tf/loadBalancerIPs: 172.50.0.80
          spec:
            type: LoadBalancer
        - metadata:
            annotations:
              metallb.universe.tf/address-pool: designateext
              metallb.universe.tf/allow-shared-ip: designateext
              metallb.universe.tf/loadBalancerIPs: 172.50.0.81
          spec:
            type: LoadBalancer
    replicas: 2
    storageClass: local-storage    # Appropriate storageClass environment specific
    storageRequest: 10G
```

> It is recommended to have a services override for each replica. In the above
> example, the replica count is two so there are 2 services defined. If replicas
> were 3 an additional service should be defined.

### designateMdns

Add the following under the designateMdns section:

```yaml
    controlNetworkName: designate  # FR2 only
    customServiceConfig: |
      # For FR2 Only.
      [oslo_policy]
      enforce_scope = False
      enforce_new_defaults = False
    networkAttachments:
      - designate
```

### designateWorker

Add the following under the designateWorker section:

```yaml
    customServiceConfig: |
      # For FR2 Only.
      [oslo_policy]
      enforce_scope = False
      enforce_new_defaults = False
    networkAttachments:
      - designate
```

### designateProducer

Add the following under the designateProducer section:

```yaml
    customServiceConfig: |
      # For FR2 Only.
      [oslo_policy]
      enforce_scope = False
      enforce_new_defaults = False
```

### designateCentral

Add the following under the designateCentral section:

```yaml
    customServiceConfig: |
      # For FR2 Only.
      [oslo_policy]
      enforce_scope = False
      enforce_new_defaults = False
```



### designateUnbound

The Unbound resolver will typically require the most user specific
configuration to get the most out of.

A typical section for Unbound might look like:
```yaml
    defaultConfigOverwrite:
      01-unbound.conf: |
        server:
          verbosity: 2
          interface: 0.0.0.0
          access-control: 172.28.0.0/24 allow
          access-control: 100.64.0.0/10 allow
          module-config: "iterator"
      forwarders.conf: |
        forward-zone:
          name: "."
          forward-addr: 172.11.5.155
          forward-addr: 172.12.5.155
        stub-zone:
          name: "mycloud.com"
          stub-addr: 172.28.0.98
          stub-addr: 172.28.0.100
   networkAttachments:
      - designate
   override:
     services:
       - metadata:
           annotations:
             metallb.universe.tf/address-pool: designateext
             metallb.universe.tf/allow-shared-ip: designateext
             metallb.universe.tf/loadBalancerIPs: 172.50.0.90
         spec:
           type: LoadBalancer
       - metadata:
           annotations:
             metallb.universe.tf/address-pool: designateext
             metallb.universe.tf/allow-shared-ip: designateext
             metallb.universe.tf/loadBalancerIPs: 172.50.0.91
         spec:
           type: LoadBalancer
   replicas: 2
```

> It is recommended to have a services override for each replica. In the above
> example, the replica count is two so there are 2 services defined. If replicas
> were 3 an additional service should be defined.

If you wish to configure stub-zones that forward particular requests to the
designate managed BIND servers, the unbound server must be connected to the
*designate* network attachment. To add the network attachment, include:

```yaml
networkAttachments:
  - designate
```


#### defaultConfigOverwrite

Everything in this section is directly related to the configuration of the
Unbound resolver service itself by creating a map of filename -> config block.
In the example above, the result is 2 configuation files mounted into each pod
under /etc/designate/conf.d, 01-unbound.conf and forwarders.conf. These files
will contain:

```conf
server:
  verbosity: 2
  interface: 0.0.0.0
  access-control: 172.28.0.0/24 allow
  access-control: 100.64.0.0/10 allow
  module-config: "iterator"
```

and

```conf
forward-zone:
  name: "."
  forward-addr: 172.11.5.155
  forward-addr: 172.12.5.155
stub-zone:
  name: "mycloud.com"
  stub-addr: 172.28.0.98
  stub-addr: 172.28.0.100
```

respectively. Any valid unbound configuration can be included in this way.

The example configuration is elaborated on below.

##### For FR2 branch only

The Unbound install contains a default configuration file which hasn't been
replaced in the FR2 release so it is necessary to disable some of the default
features when configuring the unbound resolver. We recommend disabling DNSSEC
by adding the following to the *server* clause in an unbound configuration
override, e.g.:

```yaml
server:
  ...
  module-config: "iterator"
```

##### Allow Clauses

Deployment dependent!!! The allow clauses will depend on details of your
cluster configuration that the operator cannot determine with certainty.

The default CIDR for the "join" network in OpenShift is 100.64.0.0/10. This
will be the apparent source IP when connecting to the unbound resolver. Unless
using a non-default OpenShift network configuration, the following should be
included in the server clause of the unbound configuration:

```yaml
server:
  ...
  access-control: 100.64.0.0/10 allow
  access-control: 172.28.0.0/24 allow
```

##### Forwarders

It is expected that a network will have a preferred default DNS server,
possibly blocking requests to others. For example:

```yaml
forward-zone:
  name: "."
  forward-addr: 172.11.5.155
  forward-addr: 172.12.5.155
```

##### Stub-Zones

Stub zones redirect queries for a particular zone to the provided servers. To
get the correct IP addresses for an RHOSO deployment run the following command.

```sh
oc get cm -o jsonpath='{.data}' designate-bind-ip-map | yq -P | gawk -e '{ print $2  }'
172.28.0.98
172.28.0.100
```

* Note: for this configuration to work, the unbound server must have the
**designate** network in the list of **networkAttachments** *


## Enable Designate

With all the configuration now in place, designate can be enabled by setting
'enabled' to 'true' in the OpenstackControlPlane instance.  After a short
while, several pods with the prefix of ‘designate-’ should start running, e.g:

```sh
oc get pods | grep designate

designate-api-7d8447bc98-cfl22           1/1     Running     0          10s
designate-backendbind9-0                 1/1     Running     0          15s
designate-backendbind9-1                 1/1     Running     0          20s
designate-central-86c558fb98-82bn2       1/1     Running     0          12s
designate-mdns-0                         1/1     Running     0          13s
designate-producer-7f69498d75-6wlr8      1/1     Running     0          12s
designate-redis-redis-0                  2/2     Running     0          2m
designate-unbound-0                      1/1     Running     0          14s
designate-unbound-1                      1/1     Running     0          15s
designate-worker-b9bb6dcf7-fhgr5         1/1     Running     0          11s
```

*(names and number of pods may vary)*

You can also check the deployment is completed by running the following command:

```sh
oc get Designate/designate -o jsonpath='{.status.conditions}' |
      jq -r 'map(select(.type == "Ready"))[0].status'
```

This will return ‘True’ if the Designate deployment is ready.


### Designate control plane example

```yaml
designate:
  apiOverride:
    route: {}
  enabled: true
  template:
    apiTimeout: 120
    backendMdnsServerProtocol: ""
    backendType: ""
    backendWorkerServerProtocol: ""
    customServiceConfig: '# add your customization here'
    databaseAccount: designate
    databaseInstance: openstack
    designateAPI:
      apiTimeout: 0
      backendMdnsServerProtocol: ""
      backendType: ""
      backendWorkerServerProtocol: ""
      databaseAccount: designate
      override:
        service:
          internal:
            metadata:
              annotations:
                metallb.universe.tf/address-pool: internalapi
                metallb.universe.tf/allow-shared-ip: internalapi
                metallb.universe.tf/loadBalancerIPs: 172.17.0.80
            spec:
              type: LoadBalancer
      passwordSelectors:
        service: DesignatePassword
      replicas: 1
      resources: {}
      secret: ""
      serviceAccount: ""
      serviceUser: designate
      tls:
        api:
          internal: {}
          public: {}
    designateBackendbind9:
      backendMdnsServerProtocol: ""
      backendType: ""
      backendWorkerServerProtocol: ""
      controlNetworkName: designate
      databaseAccount: designate
      netUtilsImage: ""
      networkAttachments:
        - designate
      override:
        services:
          - metadata:
              annotations:
                metallb.universe.tf/address-pool: designateext
                metallb.universe.tf/allow-shared-ip: designateext
                metallb.universe.tf/loadBalancerIPs: 172.50.0.80
            spec:
              type: LoadBalancer
          - metadata:
              annotations:
                metallb.universe.tf/address-pool: designateext
                metallb.universe.tf/allow-shared-ip: designateext
                metallb.universe.tf/loadBalancerIPs: 172.50.0.81
            spec:
              type: LoadBalancer
      passwordSelectors:
        service: DesignatePassword
      replicas: 2
      resources: {}
      secret: ""
      serviceAccount: ""
      serviceUser: designate
      storageClass: local-storage
      storageRequest: 10G
    designateCentral:
      backendMdnsServerProtocol: ""
      backendType: ""
      backendWorkerServerProtocol: ""
      databaseAccount: designate
      passwordSelectors:
        service: DesignatePassword
      replicas: 1
      resources: {}
      secret: ""
      serviceAccount: ""
      serviceUser: designate
      tls: {}
    designateMdns:
      backendMdnsServerProtocol: ""
      backendType: ""
      backendWorkerServerProtocol: ""
      controlNetworkName: designate
      databaseAccount: designate
      netUtilsImage: ""
      networkAttachments:
        - designate
      passwordSelectors:
        service: DesignatePassword
      replicas: 1
      resources: {}
      secret: ""
      serviceAccount: ""
      serviceUser: designate
      tls: {}
    designateNetworkAttachment: designate
    designateProducer:
      backendMdnsServerProtocol: ""
      backendType: ""
      backendWorkerServerProtocol: ""
      databaseAccount: designate
      passwordSelectors:
        service: DesignatePassword
      replicas: 1
      resources: {}
      secret: ""
      serviceAccount: ""
      serviceUser: designate
      tls: {}
    designateUnbound:
      defaultConfigOverwrite:
        01-unbound.conf: |
          server:
            verbosity: 2
            access-control: 172.28.0.0/24 allow
            access-control: 100.64.0.0/10 allow
            module-config: "iterator"
        forwarders.conf: |
          forward-zone:
            name: "."
            forward-addr: 172.11.5.155
            forward-addr: 172.12.5.155
          stub-zone:
            name: "example.com"
            stub-addr: 172.28.0.98
            stub-addr: 172.28.0.100
      networkAttachments:
        - designate
      override:
        services:
          - metadata:
              annotations:
                metallb.universe.tf/address-pool: designateext
                metallb.universe.tf/allow-shared-ip: designateext
                metallb.universe.tf/loadBalancerIPs: 172.50.0.90
            spec:
              type: LoadBalancer
          - metadata:
              annotations:
                metallb.universe.tf/address-pool: designateext
                metallb.universe.tf/allow-shared-ip: designateext
                metallb.universe.tf/loadBalancerIPs: 172.50.0.91
            spec:
              type: LoadBalancer
      replicas: 2
      resources: {}
      serviceAccount: ""
    designateWorker:
      backendMdnsServerProtocol: ""
      backendType: ""
      backendWorkerServerProtocol: ""
      databaseAccount: designate
      networkAttachments:
        - designate
      passwordSelectors:
        service: DesignatePassword
      replicas: 1
      resources: {}
      secret: ""
      serviceAccount: ""
      serviceUser: designate
      tls: {}
    passwordSelectors:
      service: DesignatePassword
    preserveJobs: false
    rabbitMqClusterName: rabbitmq
    redisServiceName: designate-redis
    resources: {}
    secret: osp-secret
    serviceUser: designate
```

## Enabling Neutron-Designate integration

Once Designate is deployed, neutron-dns integration can be enabled in Neutron
by adding some additional configuration to the Neutron service in the current
OpenStackControlPlane instance. The method of applying the changes is up the
reader but this document will assume the contents are applied either to the
source yaml or through the appropriate edit command with the proper
indentation.

In the neutron section, add the following (taking note of values that you must
provide) to either the *customServiceConfig* or add an entry to
*defaultConfigOverwrite*:

```conf
[DEFAULT]
dns_domain = mycloud.com.  # Use a value appropriate to your deployment
external_dns_driver = designate
[designate]
url = https://designate-public-openstack.apps-crc.testing/
auth_type = password
auth_url = https://keystone-public-openstack.apps-crc.testing
username = designate
password = PASSWORD # replace with actual password
project_name = service
project_domain_name = Default
user_domain_name = Default
allow_reverse_dns_lookup = True
ipv4_ptr_zone_prefix_size = 24
ipv6_ptr_zone_prefix_size = 116
ptr_zone_email = admin@mycloud.com
cafile = /etc/pki/ca-trust/extracted/pem/tls-ca-bundle.pem  # assuming TLS
```

If editing the OpenStackControlPlane directly, this might look like this:

```yaml
  neutron:
    template:
      customServiceConfig: |
        [DEFAULT]
        dns_domain = mycloud.com.
        external_dns_driver = designate
        [designate]
        url = https://designate-public-openstack.apps-crc.testing/
        auth_type = password
        auth_url = https://keystone-public-openstack.apps-crc.testing
        username = designate
        password = myexamplepassword
        project_name = service
        project_domain_name = Default
        user_domain_name = Default
        allow_reverse_dns_lookup = True
        ipv4_ptr_zone_prefix_size = 24
        ipv6_ptr_zone_prefix_size = 116
        ptr_zone_email = admin@mycloud.com
        cafile = /etc/pki/ca-trust/extracted/pem/tls-ca-bundle.pem
```
