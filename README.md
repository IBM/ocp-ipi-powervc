# PowerVC-Tool
A useful tool to create and check OpenShift clusters on IBM Cloud PowerVC.

To install an OpenShift cluster, please head to the main documentation root [here](https://github.com/IBM/ocp-ipi-powervc/tree/main/docs).

CLI options:
- [check-alive](https://github.com/IBM/ocp-ipi-powervc/tree/main#check-alive)
- [create-bastion](https://github.com/IBM/ocp-ipi-powervc/tree/main#create-bastion)
- [create-cluster](https://github.com/IBM/ocp-ipi-powervc/tree/main#create-cluster)
- [create-rhcos](https://github.com/IBM/ocp-ipi-powervc/tree/main#create-rhcos)
- [send-metadata](https://github.com/IBM/ocp-ipi-powervc/tree/main#send-metadata)
- [watch-create](https://github.com/IBM/ocp-ipi-powervc/tree/main#watch-create)
- [watch-installation](https://github.com/IBM/ocp-ipi-powervc/tree/main#watch-installation)

## check-alive

This will check if the [controller](https://github.com/IBM/ocp-ipi-powervc/blob/main/docs/controller.md) is alive.

Example usage:
```
$ ocp-ipi-powervc-linux-amd64 check-alive --serverIP ${controller_ip} -shouldDebug false
```

args:
- `serverIP` The IP address of the controller.

- `shouldDebug` defauts to `false`.  This will cause the program to output verbose debugging information.

## create-bastion

This will create an HAProxy VM which will act as an OpenShift Load Balancer.  This VM will be managed by another instance of this program with the `watch-installation` parameter.

NOTE:
The environment variable `IBMCLOUD_API_KEY` is optional.  If not set, make sure DNS is supported via CoreOS DNS or another method.

Example usage:

```
$ ocp-ipi-powervc-linux-amd64 create-bastion --cloud ${cloud_name} --bastionName ${bastion_name} --flavorName ${flavor_name} --imageName ${image_name} --networkName ${network_name} --sshKeyName ${ssh_keyname} --domainName ${domain_name} --enableHAProxy true --serverIP ${controller_ip} --shouldDebug true
```

args:
- `cloud` the name of the cloud to use in the `~/.config/openstack/clouds.yaml` file.

- `bastionName` The name of the VM to use which should match the OpenShift cluster name.

- `flavorName` The OpenStack flavor to create the VM with.

- `imageName` The OpenStack image to create the VM with.

- `networkName` The OpenStack network to create the VM with.

- `sshKeyName` The OpenStack ssh keyname to create the VM with.

- `domainName` The DNS domain name for the bastion. (optional)

- `enableHAProxy` defaults to `true`.  If we should install HA Proxy on the bastion node.

- `serverIP` The IP address of the controller.

- `shouldDebug` defauts to `false`.  This will cause the program to output verbose debugging information.

## create-cluster

NOTE: DEPRECATED

This was a development tool used during the initial investigation.  It takes a powervc `install-config.yaml`, converts it to a openstack configuration, calls the IPI installer, and then converts the generated files to work on a PowerVC setup.

Example usage:

```
$ ocp-ipi-powervc-linux-amd64 create-cluster --directory ${directory} --shouldDebug true
```

args:
- `directory` location to use the IPI installer

- `shouldDebug` defauts to `false`.  This will cause the program to output verbose debugging information.

## create-rhcos

This will create a test RHCOS VM.  This VM will be managed by the controller.

NOTE:
The environment variable `IBMCLOUD_API_KEY` is optional.  If not set, make sure DNS is supported via CoreOS DNS or another method.

Example usage:

```
$ ocp-ipi-powervc-linux-amd64 create-rhcos --cloud ${cloud_name} --rhcosName ${rhcos_name} --flavorName ${flavor_name} --imageName ${image_name} --networkName ${network_name} --sshPublicKey $(cat ${HOME}/.ssh/id_installer_rsa.pub) --domainName ${domain_name} --shouldDebug true
```

args:
- `cloud` the name of the cloud to use in the `~/.config/openstack/clouds.yaml` file.

- `rhcosName` The name of the VM to use which should match the OpenShift cluster name.

- `flavorName` The OpenStack flavor to create the VM with.

- `imageName` The OpenStack image to create the VM with.

- `networkName` The OpenStack network to create the VM with.

- `passwdHash` The password hash of the core user.

- `sshPublicKey` The OpenStack ssh keyname to create the VM with.

- `domainName` The DNS domain name for the bastion. (optional)

- `shouldDebug` defauts to `false`.  This will cause the program to output verbose debugging information.

## send-metadata

This will send a command to the server to either create or delete a local copy of the metadata.json file.

Example usage:

```
$ ocp-ipi-powervc-linux-amd64 send-metadata --createMetadata ${directory}/metadata.json --serverIP ${controller_ip} --shouldDebug true
```

args:

- `createMetadata` Tells the server to create a local copy of this metadata.json file.

- `deleteMetadata` Tells the server to delete a local copy of this metadata.json file.

- `serverIP` The IP address of the controller.

- `shouldDebug` defauts to `false`.  This will cause the program to output verbose debugging information.

## watch-create

NOTE:
The environment variable `IBMCLOUD_API_KEY` needs to be set.

Example usage:

```
$ ocp-ipi-powervc-linux-amd64 watch-create --metadata ${directory}/metadata.json --kubeconfig ${directory}/auth/kubeconfig --cloud ${cloud_name} --bastionUsername ${bastion_username} --bastionRsa ${HOME}/.ssh/id_installer_rsa --baseDomain ${domain_name} --shouldDebug false
```

args:
- `cloud` the name of the cloud to use in the `~/.config/openstack/clouds.yaml` file.

- `metadata` the location of the `metadata.json` file created by the IPI OpenShift installer.

- `kubeconfig` the location of the `kubeconfig` file created by the IPI OpenShift installer.

- `bastionUsername` the default username for the HAProxy VM.

- `bastionRsa` the SSH private key file for the default username for the HAProxy VM.

- `baseDomain` the domain name of the OpenShift cluster.

- `shouldDebug` defauts to `false`.  This will cause the program to output verbose debugging information.

## watch-installation

This is for checking the progress of an ongoing `openshift-install create cluster` operation of the OpenShift IPI installer.  Run this in another window while the installer deploys a cluster.

NOTE:
The environment variable `IBMCLOUD_API_KEY` is optional.  If not set, make sure DNS is supported via CoreOS DNS or another method.

Example usage:

```
$ ocp-ipi-powervc-linux-amd64 watch-installation --cloud ${cloud_name} --domainName ${domain_name} --bastionMetadata ${directory}/metadata.json --bastionUsername ${bastion_username} --bastionRsa ${HOME}/.ssh/id_installer_rsa --dhcpSubnet ${dhcp_subnet} --dhcpNetmask ${dhcp_netmask} --dhcpRouter ${dhcp_router} --dhcpDnsServers "${dhcp_servers}" --shouldDebug true
```

args:
- `cloud` the name of the cloud to use in the `~/.config/openstack/clouds.yaml` file.

- `domainName` the domain name to use for the OpenShift cluster.

- `bastionMetadata` the location of the `metadata.json` file created by the IPI OpenShift installer.  This parameter can have more than one occurance.

- `bastionUsername` the default username for the HAProxy VM.

- `bastionRsa` the SSH private key file for the default username for the HAProxy VM.

- `enableDhcpd` defaults to `false.  Enables updating the locally installed dhcp server.

- `dhcpInterface` The network interface to listen for DHCPd requests.

- `dhcpSubnet` The subnet to use for DHCPd requests.

- `dhcpNetmask` The netmask to use for DHCPd requests.

- `dhcpRouter` The router to use for DHCPd requests.

- `dhcpDnsServers` The comma separated DNS servers to use for DHCPd requests.

- `dhcpServerId` The DNS server identifier for a DHCP request.

- `shouldDebug` defauts to `false`.  This will cause the program to output verbose debugging information.

# Useful scripts

## scripts/create-cluster.sh

This script will create an OpenShift cluster using the IPI installer.

Required environment variables before running this script:

- `BASEDOMAIN` the domain name to use for the OpenShift cluster.

- `BASTION_IMAGE_NAME` the OpenStack image name for the HAProxy VM.

- `BASTION_USERNAME` the default username for the HAProxy VM.

- `BASTION_RSA` the ssh private key for the bastion node.

- `CLOUD` the name of the cloud to use in the `~/.config/openstack/clouds.yaml` file.

- `CLUSTER_DIR` the directory location where the OpenShift IPI installer will save important files.

- `CLUSTER_NAME` the name prefix to use for the OpenShift cluster which you are installing.

- `CONTROLLER_IP` the IP address of the controller.

- `FLAVOR_NAME` the OpenStack flavor name to use for OpenShift VMs.

- `INSTALLER_SSHKEY` the ssh public key for access to the bootstrap and master nodes.  Usually named `~/.ssh/id_installer_rsa.pub`.

- `MACHINE_TYPE` the PowerPC machine type to use for OpenShift VMs.

- `NETWORK_NAME` the OpenStack network name to use for OpenShift VMs.

- `PULLSECRET_FILE` the filename containing the pull secrets for the OpenShift containers. Usually named `~/.pullSecretCompact`.

- `SSHKEY_NAME` the OpenStack ssh keyname to use for the HAProxy VM.

Required existing files before running this script:

- `~/.pullSecretCompact`

- `~/.ssh/id_installer_rsa.pub`

Required existing binaries before running this script:

- `openshift-install` The OpenShift IPI installer.

- `ocp-ipi-powervc-linux-${ARCH}` This repo tool.

- `openstack` The OpenStack CLI tool existing on Fedora/RHEL/CentOS repositories.

- `jq` The JSON query CLI tool found at https://jqlang.org/download/ and existing on Fedora/RHEL/CentOS repositories.

## scripts/delete-cluster.sh

This script will delete an OpenShift cluster using the IPI installer.

Required environment variables before running this script:

- `CLUSTER_DIR` the directory location where the OpenShift IPI installer will save important files.

- `CONTROLLER_IP` the IP address of the controller.

Required existing binaries before running this script:

- `openshift-install` The OpenShift IPI installer.

- `ocp-ipi-powervc-linux-${ARCH}` This repo tool.

- `ping` a Linux admin tool.

## scripts/check-alive.sh

This script will check if this repo tool is running on the controller IP address.  If it is not, then it will start it up inside of the tmux window number 0.

Required environment variables before running this script:

- `BASEDOMAIN` the domain name to use for the OpenShift cluster.

- `BASTION_USERNAME` the default username for the HAProxy VM.

- `BASTION_RSA` the ssh private key for the bastion node.

- `CLOUD` the name of the cloud to use in the `~/.config/openstack/clouds.yaml` file.

- `CONTROLLER_IP` the IP address of the controller.

- `DHCP_DNS_SERVERS` a list of DNS servers.

- `DHCP_NETMASK` the netmask used for a DHCP request.

- `DHCP_ROUTER` the router used for a DHCP request.

- `DHCP_SERVER_ID` the DHCP server ID used for a DHCP request.

- `DHCP_SUBNET` the DHCP subnet used for a DHCP request.

Required existing binaries before running this script:

- `ocp-ipi-powervc-linux-${ARCH}` This repo tool.

- `awk` a Linux admin tool.

- `cut` a Linux admin tool.

- `ip` a Linux admin tool.

- `tmux` a linux shell windowing tool.

- `tr` a Linux admin tool.

## scripts/console.sh

This script will output the ssh command needed to access the console for a VM or OpenShift node name.

## scripts/ssh.sh

This script will output the ssh command needed to access a specific OpenShift node.
