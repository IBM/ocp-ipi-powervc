# Easy installation method

With the [controller](https://github.com/IBM/ocp-ipi-powervc/blob/main/docs/controller.md) running, there are scripts which can help you create and delete clusters.  Please write down the IP address of the controller VM.

# Create CentOS image for bastion nodes

Find the latest CentOS cloud image [here](https://cloud.centos.org/centos/10-stream/ppc64le/images/), transform it into an OVA image, then upload it to the PowerVC server.

```
$ curl --location --remote-name https://cloud.centos.org/centos/10-stream/ppc64le/images/CentOS-Stream-GenericCloud-10-20241118.0.ppc64le.qcow2
$ pvsadm image qcow2ova --image-dist coreos --image-name CentOS-Stream-GenericCloud-10-20241118.0.ppc64le --image-url /data/OVAs/CentOS-Stream-GenericCloud-10-20241118.0.ppc64le.qcow2
$ powervc-image --project ocp-ipi import -n CentOS-Stream2-GenericCloud-10-20241118.0.ppc64le -p CentOS-Stream-GenericCloud-10-20241118.0.ppc64le.ova.gz -t ... -m os-type=coreos architecture=ppc64le
$ openstack --os-cloud=powervc image list --format csv | grep CentOS-Stream2
"e8dd343e-05c2-4def-97fa-3bb098c2c2ce","CentOS-Stream2-GenericCloud-10-20241118.0.ppc64le","active"
```

# Create the ssh key for the bastion VM

```
$ openstack --os-cloud=powervc keypair create --public-key ~/.ssh/id_installer_rsa.pub bastion-key
```

# Determine the OpenStack machine type

```
$ openstack --os-cloud=powervc availability zone list --format csv
"Zone Name","Zone Status"
"s1022","available"
```

# Determine the OpenStack network id

```
$ openstack --os-cloud=powervs network list --format csv
"ID","Name","Subnets"
"1762f355-b17e-4d13-9bca-d5b53c929ab0","vlan1337","['ae643a65-d0fc-4408-90c6-a820340bfade']"
```

# Create the OpenStack flavor

```
$ openstack --os-cloud=powervc flavor create ocp-ipi --ram 32768 --disk 100 --vcpus 4 --public --property powervm:availability_priority=127 --property powervm:dedicated_proc=false --property powervm:max_mem=34816 --property powervm:max_proc_units=1 --property powervm:max_vcpu=4 --property powervm:min_mem=16384 --property powervm:min_proc_units=1 --property powervm:min_vcpu=4 --property powervm:proc_units=1 --property powervm:shared_weight=128 --property powervm:uncapped=true
```

# Create the pull secret

Download your pull-secret from https://console.redhat.com/openshift/install/pull-secret

Copy it to ~/.pullSecret

```
$ tr -d '[:space:]' < ~/.pullSecret > ~/.pullSecretCompact
```

# Create environment variables

It helps to have environment variables set up before creation so you are not asked these questions.  Either export them in the bash shell or ceate a file with them and source the contents.

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

# Download the OpenShift installer for a release

```
$ (mkdir 4.22.0-ec.2; cd 4.22.0-ec.2/; oc adm -a ${HOME}/.pullSecretCompact release extract --tools quay.io/openshift-release-dev/ocp-release:4.22.0-ec.2-ppc64le; tar xvzf openshift-install-linux-amd64-4.22.0-ec.2.tar.gz)
```

# Install jq

```
$ sudo dnf install -y jq
```

# Install yq

```
$ (cd ~/bin/; curl --output yq --location https://github.com/mikefarah/yq/releases/download/v4.48.1/yq_linux_ppc64le && chmod u+x yq)```

# Create an OpenShift cluster

With these set, you can now run `scripts/create-cluster.sh`.  For example:

```
$ (export PATH=${PATH}:${HOME}/ocp-ipi-powervc/:${HOME}/4.22.0-ec.2/; source environment; [ -d "${CLUSTER_DIR}" ] && rm -rf "${CLUSTER_DIR}/"; ${HOME}/ocp-ipi-powervc/scripts/create-cluster.sh)
```

# Delete an OpenShift cluster

```
$ (export PATH=${PATH}:${HOME}/ocp-ipi-powervc/:${HOME}/4.22.0-ec.2/; source environment; [ ! -d "${CLUSTER_DIR}" ] && exit 1; ${HOME}/ocp-ipi-powervc/scripts/delete-cluster.sh)
```
