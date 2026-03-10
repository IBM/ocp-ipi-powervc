# Environment variables

It helps to have environment variables set up before creation so you are not asked these questions during installation.  Either export them in the bash shell or create a file with them and source the contents.

The following are the individual pieces you need to gather or create for the environment variables.

## Create the ssh key for the bastion VM

When the tool creates a bastion node, it needs an ssh key.  Either create a new one or write down the id of an existing one.

```
$ openstack --os-cloud=powervc keypair create --public-key ~/.ssh/id_installer_rsa.pub bastion-key
```

## Determine the OpenStack machine type

Query the machine types and write down the id of an existing one.

```
$ openstack --os-cloud=powervc availability zone list --format csv
"Zone Name","Zone Status"
"s1022","available"
```

## Determine the OpenStack network id

Query the network id to use.

```
$ openstack --os-cloud=powervs network list --format csv
"ID","Name","Subnets"
"1762f355-b17e-4d13-9bca-d5b53c929ab0","vlan1337","['ae643a65-d0fc-4408-90c6-a820340bfade']"
```

## Create the OpenStack flavor

VMs are created with an OpenStack flavor.  Either create a new one or write down the id of an existing one.

```
$ openstack --os-cloud=powervc flavor create ocp-ipi --ram 32768 --disk 100 --vcpus 4 --public --property powervm:availability_priority=127 --property powervm:dedicated_proc=false --property powervm:max_mem=34816 --property powervm:max_proc_units=1 --property powervm:max_vcpu=4 --property powervm:min_mem=16384 --property powervm:min_proc_units=1 --property powervm:min_vcpu=4 --property powervm:proc_units=1 --property powervm:shared_weight=128 --property powervm:uncapped=true
```

# Create the pull secret

Download your pull-secret from https://console.redhat.com/openshift/install/pull-secret

Copy it to ~/.pullSecret

```
$ tr -d '[:space:]' < ~/.pullSecret > ~/.pullSecretCompact
```

## The set of environment variables used

```
export BASEDOMAIN="example.ibm.net"
export BASTION_IMAGE_NAME="CentOS-Stream2-GenericCloud-10-20241118.0.ppc64le"
export BASTION_USERNAME="cloud-user"
export BASTION_RSA=/home/${USER}/.ssh/id_installer_rsa
export CLOUD="powervc"
export CLUSTER_DIR="test"
export CLUSTER_NAME="rdr-xxx-openstack"
export FLAVOR_NAME="ocp-ipi"
export INSTALLER_SSHKEY=/home/${USER}/.ssh/id_installer_rsa.pub
export MACHINE_TYPE="s1022"
export NETWORK_NAME="vlan1337"
export PULLSECRET_FILE="/home/${USER}/.pullSecretCompact"
export SSHKEY_NAME="bastion-key"
export CONTROLLER_IP="10.20.184.56"
```
