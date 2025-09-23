# Removing designate from an OpenStack on OpenShift environment

The following instructions walk through the steps of disabling designate and
removing artifacts to allow for a clean re-start of designate in the future.

Please note that these instructions do not include the removal of designate
related cluster networking configuration.

## Backup the controlplane

_Note: the following instructions assume an openstackcontrolplane name of
openstack-galera-network-isolation. The name will likely differ in your
deployment so please adjust accordingly._

It is __highly__ recommended that you back up the OpenstackControlPlane CR
before proceeding.

```console
# oc get -n openstack openstackcontrolplane <insert control plane name here> -o yaml > openstackcontrolplane-backup.yaml
oc get -n openstack openstackcontrolplane openstack-galera-network-isolation -o yaml > openstackcontrolplane-backup.yaml
```

## Disable designate in the control plane

```console
oc patch --type=merge -n openstack  openstackcontrolplane openstack-galera-network-isolation --patch '
spec:
   designate:
     enabled: false
'

# wait until done
oc wait --for=delete -n openstack designate/designate

```

## Disable designate-redis

Verify name of the designate Redis instance. Note that there may be other Redis
instances, do not remove them or it may destabilize other deployed OpenStack
services.

```console
oc get -n openstack redis
NAME              STATUS   MESSAGE
designate-redis   True     Setup complete
```

To remove the __designate-redis__ instance, run the following command (_note:
change the 'designate-redis' string to the correct name if different than the
defaults_)

```console
oc patch --type merge -n openstack openstackcontrolplane openstack-galera-network-isolation --type json -p='[{"op": "remove", "path": "/spec/redis/templates/designate-redis"}]'
oc wait --for=delete -n openstack redis/designate-redis
```

Now check that it is removed.

```console
# After removal both the pods and the Designate Redis instance should be gone.
oc get -n openstack pods | grep designate-redis   # should give no results
oc get -n openstack redis | grep designate        # should give no results
```

## Remove leftover designate services

The operator may not complete remove all of the services it creates when
disabling designate. It is a good idea to check and, if necessary, remove them
manually. For example:

```console
oc get -n openstack svc -l 'component in (designate-backendbind9, designate-unbound)'
NAME                       TYPE           CLUSTER-IP     EXTERNAL-IP      PORT(S)                     AGE
designate-backendbind9-0   LoadBalancer   10.217.4.49    172.19.0.80      53:31722/UDP,53:31722/TCP   8m8s
designate-backendbind9-1   LoadBalancer   10.217.5.175   172.17.0.82      53:32183/UDP,53:32183/TCP   8m8s
designate-backendbind9-2   LoadBalancer   10.217.5.51    192.168.122.81   53:31431/UDP,53:31431/TCP   8m7s
designate-unbound-0        LoadBalancer   10.217.4.230   172.17.0.81   53:32688/UDP,53:32688/TCP   9m12s
```

Run command

```console
oc delete -n openstack svc -l 'component in (designate-backendbind9, designate-unbound)'
service "designate-backendbind9-0" deleted
service "designate-backendbind9-1" deleted
service "designate-backendbind9-2" deleted
service "designate-unbound-0" deleted
```

```console
oc get -n openstack svc | grep designate   # should give no results
```

## Check for and remove remnant configmaps and secrets

Check to make sure any secrets created by designate have been cleaned up.

```console
oc get cm -n openstack |  grep designate # should give no results
```

There is a secret that is currently left behind after deployment. To remove it, run:

```console
oc delete secret designate-bind-secret -n openstack
```

## Check for unreleased persistent volume claims

In some situations, persistent volumes are not always fully freed and may
prevent persistent volume claims from succeeding in future deployments. To
release them, run the following command.

```console
oc get pv -n openstack | grep Released | cut -f 1 -d ' ' | while read; do oc patch pv $REPLY -n openstack -p '{"spec":{"claimRef": null}}'; done
```

## Check keystone

Double check that the keystone entry has been removed

```console
oc rsh -n openstack openstackclient openstack endpoint list | grep dns   # should return no results
```

## Remove designate db from galera

In general, databases for services in OpenStack are not deleted _by design_ in
the event that a service is accidentally disabled. Removing the database is
recommended if the goal is to re-enable a fresh designate deployment. __DO
NOT__ perform this step if you wish to restart with existing zones, recordsets,
etc. However, clearing the state of the BIND9 servers and re-enabling with
an existing database has not been tested.  The following is an example on how
to remove the designate database.

```console
oc get secret -n openstack osp-secret -o jsonpath='{.data.DbRootPassword}' | base64 -d
iAmTheRootDbPassword

oc exec -n openstack -it openstack-galera-0 -- mysql -u root -p
Enter Password: iAmTheRootDbPassword

Welcome to the MariaDB monitor.  Commands end with ; or \g.
Your MariaDB connection id is 1128608
Server version: 10.5.27-MariaDB MariaDB Server

Copyright (c) 2000, 2018, Oracle, MariaDB Corporation Ab and others.

Type 'help;' or '\h' for help. Type '\c' to clear the current input statement.

mariaDB [(none)]> show databases like '%designate%';
+------------------------+
| Database (%designate%) |
+------------------------+
| designate              |
+------------------------+
1 row in set (0.001 sec)

mariaDB[(none)]> drop database designate;
Query OK, 23 rows affected (0.046 sec).  # example output may differ

mariaDB[(none)]> exit
```

## Remove queues remaining in rabbitmq

This will remove any designate related queues remaining in rabbitmq.

```console
oc exec -n openstack  -it rabbitmq-server-0 -- /bin/bash -c 'for q in `rabbitmqctl list_queues name | grep designate`; do rabbitmqctl delete_queue $q; done'
```

## Remove neutron-designate integration configuration from neutron

remove the following from the neutron customServiceConfig by editing the OpenstackControlPlane CR

```console
oc edit -n openstack openstackcontrolplane openstack-galera-network-isolation
```

```console
[DEFAULT]
dns_domain = <whatever is there>
external_dns_driver = designate

[designate]
... all entries in this section

```

## Re-enabling designate

_Note: this is not a required step. It is just to verify that designate will
come back cleanly.  Certain things that were removed will need to be manually
added back to the contol plane to recover the initial state (e.g. the
  neutron-designate configuration in neutron) and are not covered here_

Why re-enable? One of _proofs_ of a clean uninstall is that you should be able
to "re-install" designate with 0 left overs, corruptions or conflicting
pre-existing items in the way.

```console
oc patch --type merge -n openstack openstackcontrolplane openstack-galera-network-isolation --type json -p='[{"op": "add", "path": "/spec/redis/templates/designate-redis", "value": {"designate-redis":  { "replicas" : 1 }}}]'

oc patch --type=merge -n openstack  openstackcontrolplane openstack-galera-network-isolation --patch '
 spec:
   designate:
     enabled: true
'
sleep 10 # to allow time for OpenShift to create the initial designate object before running the next line
# This might fail the first few times if the system is very loaded.
oc wait --for=condition=Ready=true designate/designate --timeout=240s
```
