# Debugging

Deploying an OpenShift cluster on the PowerVC platform is comprised of a number of steps.  Each step must succeed before a cluster can be created.

1) The PowerVC server has an RHCOS image for the current release
2) The controller VM should be up and running with one window running the ocp-ipi-powervc program.
3) The bastion node should be up and running with haproxy process running.
4) DNS entries have been created
5) The controller VM should have the metadata.json of the new cluster
6) The bootstrap, master-0, master-1, and master-2 nodes should have been successfully created in OpenStack
7) The bootstrap, master-0, master-1, and master-2 nodes should have received a DHCP IP
8) The console for the bootstrap VM should have a login prompt
9) Ssh into the bootstrap VM and check the journalctl
10) The console for the master-0, master-1, and master-2 VMs should have a login prompt
11) Ssh in master-0, master-1, and master-2 nodes and see crictl processes running
12) Check the status of cluster operators in kubernetes
13) Check the status of nodes in kubernetes
14) Run the ocp-ipi-powervc helper program to display the status of the cluster

## The PowerVC server has an RHCOS image for the current release

Find out the version of the RHCOS image used.

```
$ (URL=$(openshift-install coreos print-stream-json | jq -r '.architectures.ppc64le.artifacts.openstack' | jq -r '.formats."qcow2.gz".disk.location'); echo "URL=${URL}"; FILENAME="${URL##*/}"; FILENAME=${FILENAME//.qcow2.gz/}; echo "FILENAME=${FILENAME}")
URL=https://rhcos.mirror.openshift.com/art/storage/prod/streams/rhel-9.6/builds/9.6.20251023-0/ppc64le/rhcos-9.6.20251023-0-openstack.ppc64le.qcow2.gz
FILENAME=rhcos-9.6.20251023-0-openstack.ppc64le
```

See if that image exists in OpenStack.

```
$ openstack --os-cloud=${CLOUD} image show rhcos-9.6.20251023-0-openstack.ppc64le --format shell
```

Test an RHCOS VM deploy.

```
$ ocp-ipi-powervc-linux-${ARCH} create-rhcos --cloud "${CLOUD}" --rhcosName test-rhcos --imageName rhcos-9.6.20251023-0-openstack.ppc64le --flavorName "${FLAVOR_NAME}" --networkName "${NETWORK_NAME}" --sshPublicKey "$(cat ~/.ssh/id_installer_rsa.pub)" --passwdHash '...' --domainName "${BASEDOMAIN}" --shouldDebug true)
```

## The controller VM should be up and running with one window running the ocp-ipi-powervc program.

There should be an PowerVC VM where the ocp-ipi-powervc program runs.  I suggest using tmux to host the windows.

```
$ ocp-ipi-powervc-linux-${ARCH} check-alive --serverIP "${CONTROLLER_IP}"
```

## The bastion node should be up and running with haproxy process running.

Check on the status of the haproxy daemon with ssh.

```
$ ssh -i ${HOME}/.ssh/id_bastion cloud-user@${BASTION_IP} sudo systemctl status haproxy.service --no-pager -l
```

## DNS entries have been created

Check that the OpenShift entry points have DNS entries.

```
$ getent ahostsv4 api.${CLUSTER_NAME}.${BASEDOMAIN}
$ getent ahostsv4 api-int.${CLUSTER_NAME}.${BASEDOMAIN}
$ getent ahostsv4 console.apps.${CLUSTER_NAME}.${BASEDOMAIN}
```

## The controller VM should have the metadata.json of the new cluster

On the controller VM, list the directory contents where the bastion metadata is stored. This directory was specified by the
`--bastionMetadata` parameter.  Verify that a new directory has been created with the cluster name and that it contains a valid JSON file called metadata.json.

## The bootstrap, master-0, master-1, and master-2 nodes should have been successfully created in OpenStack

List the PowerVC servers via the following OpenStack CLI:

```
$ openstack --os-cloud=${CLOUD} server list --format csv
```

Or go to the UI (`Virtual Machines` -> `VM list`).  Make sure the state is `ACTIVE`.

## The bootstrap, master-0, master-1, and master-2 nodes should have received a DHCP IP

Use the CLI or UI and make sure the VMs are assigned IP addresses.

On the controller VM, make sure that the dhcpd service is running:

```
# systemctl status dhcpd.service --no-pager -l
```

Then make sure that the dhcpd server assigned the DHCP IP addresses:

```
# journalctl --no-pager --boot --unit=dhcpd | egrep $(echo -n '('; grep 'hardware ethernet' /etc/dhcp/dhcpd.conf | sed -r -e 's,^[^f]*([^;]*);.*$,\1,' | paste -sd '|' | tr -d '\n'; echo -n ')')
```

## The console for the bootstrap VM should have a login prompt

Use the script `console.sh` to get the ssh command which will log into the HMC or NovaLink controller and access the specific VM's console.

```
$ CLUSTER_DIR=test ./scripts/console.sh bootstrap
```

NOTE: Use the following key presses to escape the session: <~> + <.> + <Enter>

## Ssh into the bootstrap VM and check the journalctl

Use the script `ssh.sh` to ssh into the bootstrap VM.  Once inside, use `journalctl` to view the logs.

```
$ ./scripts/ssh.sh bootstrap
[core@bootstrap ~]$ journalctl -b -f -u release-image.service -u bootkube.service -u node-image-pull.service
```

## The console for the master-0, master-1, and master-2 VMs should have a login prompt

Use the same process as the console bootstrap for the master-0, master-1, and master-2 VMs.

## Ssh in master-0, master-1, and master-2 nodes and see crictl processes running

Use the same process as the ssh bootstrap for the master-0, master-1, and master-2 VMs.

```
[core@master-0 ~]$ sudo crictl ps
[core@master-0 ~]$ sudo crictl logs ${uuid}
```

## Check the status of cluster operators in kubernetes

Wait for the IPI installer to get to the following point:

```
INFO Waiting up to 15m0s (until 7:39AM CST) for network infrastructure to become ready...
INFO Network infrastructure is ready
INFO Control-plane machines are ready
INFO Waiting up to 20m0s (until 7:46AM CST) for the Kubernetes API at https://api.cluster-name.base-domain:6443... 
```

Then, in another window, use the OpenShift client (`oc`) to view the status of the cluster operators.

```
$ KUBECONFIG=${CLUSTER_DIR}/auth/kubeconfig oc get co
```

## Check the status of nodes in kubernetes

```
$ KUBECONFIG=${CLUSTER_DIR}/auth/kubeconfig oc get nodes -o wide
```

## Run the ocp-ipi-powervc helper program to display the status of the cluster

```
$ ocp-ipi-powervc-linux-${ARCH} \
	watch-create \
	--cloud ${CLOUD} \
	--metadata ${CLUSTER_DIR}/metadata.json \
	--kubeconfig ${CLUSTER_DIR}/auth/kubeconfig \
	--bastionUsername ${BASTION_USERNAME} \
	--bastionRsa ${BASTION_RSA} \
	--baseDomain ${BASEDOMAIN} \
	--shouldDebug false
```

## General OpenShift debugging commands:

```
$ export KUBECONFIG=${CLUSTER_DIR}/auth/kubeconfig
$ oc --request-timeout=5s get clusterversion
$ oc --request-timeout=5s get co
$ oc --request-timeout=5s get nodes -o=wide
$ oc --request-timeout=5s get pods -n openshift-machine-api
$ oc --request-timeout=5s get machines.machine.openshift.io -n openshift-machine-api
$ oc --request-timeout=5s get machineset.machine.openshift.io -n openshift-machine-api
$ oc --request-timeout=5s logs -l k8s-app=controller -c machine-controller -n openshift-machine-api
$ oc --request-timeout=5s describe co/cloud-controller-manager
$ oc --request-timeout=5s describe cm/cloud-provider-config -n openshift-config
$ oc --request-timeout=5s get pod -l k8s-app=cloud-manager-operator -n openshift-cloud-controller-manager-operator
$ oc --request-timeout=5s get pods -n openshift-cloud-controller-manager-operator
$ oc --request-timeout=5s describe pod -l k8s-app=openstack-cloud-controller-manager -n openshift-cloud-controller-manager
$ oc --request-timeout=5s get events -n openshift-cloud-controller-manager
$ oc --request-timeout=5s -n openshift-cloud-controller-manager-operator logs deployment/cluster-cloud-controller-manager-operator -c cluster-cloud-controller-manager
$ oc --request-timeout=5s get co/network
$ oc --request-timeout=5s get co/kube-controller-manager
$ oc --request-timeout=5s get co/etcd
$ oc --request-timeout=5s get machines.machine.openshift.io -n openshift-machine-api
$ oc --request-timeout=5s get machineset.m -n openshift-machine-api
$ oc --request-timeout=5s get pods -n openshift-machine-api
$ oc --request-timeout=5s get pods -n openshift-kube-controller-manager
$ oc --request-timeout=5s get pods -n openshift-ovn-kubernetes
$ oc --request-timeout=5s describe co/machine-config
$ oc --request-timeout=5s get pods -A -o=wide | sed -e "/\(Running\|Completed\)/d"
$ oc --request-timeout=5s get csr | grep Pending
```
