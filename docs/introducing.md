# Authors
Authors: Mike Turek, Mark Hamzy and Paul Bastide, The Red Hat OpenShift Container Platform on IBM Power team.

# Introduction
With Red Hat OpenShift Container Platform (OCP) 4.21, there is a new Tech Preview of powervc platform. The technical preview provides early access to this installer platform. The technical preview enables you to test and provide feedback on the simplified deployment of OCP on IBM Power Virtual Center (PowerVC) managed infrastructure, offering a powerful combination of enterprise-grade reliability and container orchestration. For administrators using IBM PowerVC, the Installer Provisioned Infrastructure (IPI) method simplifies the deployment process by automating the provisioning of underlying infrastructure resources.

This article introduces you to the step-by-step process of setting up an OpenShift cluster on IBM PowerVC using the IPI installer. This article is ideally suited to those familiar with IBM PowerVC and with administrative access to IBM PowerVC.

# Prerequisite Versions

IPI PowerVC is tested with IBM PowerVC 2.3.1 and OpenShift 4.21.0 and higher.

# Prerequisite Setups 

Before starting the installation, ensure you have the following tools installed and configured on your management machine (bastion).

1. Configure the PowerVC Root CA

If your IBM PowerVC uses a custom Root CA (specifically for the server at 10.20.27.10), you must register it so the OpenStack client can communicate securely.

Download the PEM file for your PowerVC instance to the anchors folder

```
# echo "" | openssl s_client -showcerts -prexit -connect my-powervc.ibm.net:443 2> /dev/null | sed -n -e '/BEGIN CERTIFICATE/,/END CERTIFICATE/ p' > /etc/pki/ca-trust/source/anchors/my-powervc-443.crt
```

Check to see that it is a PEM file

```
# file /etc/pki/ca-trust/source/anchors/my-powervc-443.crt
/etc/pki/ca-trust/source/anchors/my-powervc-443.crt: PEM certificate
```

Update the System Trust Store

```
# update-ca-trust
```

You can verify (no -k used)

```
# curl https://mypowervc.ibm.net:8443 -o/dev/null
  % Total    % Received % Xferd  Average Speed   Time    Time     Time  Current
                                 Dload  Upload   Total   Spent    Left  Speed
100 78321  100 78321    0     0  3186k      0 --:--:-- --:--:-- --:--:-- 3186k
```

Copy the powervc cert into the right location.

```
# mkdir -p ~/.config/openstack/
# cp /etc/pki/ca-trust/source/anchors/my-powervc-443.crt ~/.config/openstack/
```

You've setup the trust between the openshift-installer and PowerVC.

2. Download Tools

You need the oc client, the openshift-install binary, the OpenStack CLI, and the IBM Cloud CLI.

A. OpenShift Client (oc) Ensure you are using the latest release for ppc64le or your architecture.

```
$ curl --silent --location https://mirror.openshift.com/pub/openshift-v4/ppc64le/clients/ocp/stable-4.21/openshift-client-linux.tar.gz -o /tmp/openshift-client-linux.tar.gz
$ sudo tar -C /usr/local/bin/ -xvf /tmp/openshift-client-linux.tar.gz oc kubectl
```

Confirm oc is functional, type oc version.

B. OpenShift Installer (openshift-install)

Download the openshift-install

```
$ curl --silent --location https://mirror.openshift.com/pub/openshift-v4/ppc64le/clients/ocp/stable-4.21/openshift-install-linux.tar.gz -o /tmp/openshift-install-linux.tar.gz
$ sudo tar -C /usr/local/bin/ -xvf openshift-install-linux.tar.gz openshift-install
```

C. OpenStack CLI

Install the OpenStack client to interact with PowerVC.

Note: These commands work on CentOS Stream 9.

```
$ sudo dnf install -y dnf-plugins-core
$ sudo dnf config-manager --set-enabled crb
$ sudo dnf install -y centos-release-openstack-dalmatian
$ sudo dnf install -y python3-openstackclient
```

D. IBM Cloud CLI

This is required for managing DNS records via IBM Cloud Internet Services (CIS). You should register the records in your system.

```
$ curl -fsSL https://clis.cloud.ibm.com/install/linux | sh
$ for PLUGIN in dns cis; do ibmcloud plugin install ${PLUGIN}; done
```

E. PowerVC-Tool Helper

Download the PowerVC-Tool, a Go-based helper utility that automates bastion creation, DHCP updates, and installation monitoring.

```
$ curl --remote-name --location https://github.com/hamzy/PowerVC-Tool/releases/download/v0.9.2/PowerVC-Tool-v0.9.2-linux-ppc64le.tar.gz
$ tar xvzf PowerVC-Tool-v0.9.2-linux-ppc64le.tar.gz
$ export PATH=${PATH}:/home/cloud-user
```

# Environment Configuration

Set up your shell environment variables. These will be used throughout the installation script. Replace CLUSTER_NAME with your specific identifier.

```
$ export BASEDOMAIN="example.ibm.net"
$ export BASTION_IMAGE_NAME="CentOS-Stream2-GenericCloud-10-20241118.0.ppc64le"
$ export BASTION_USERNAME="cloud-user"
$ export CLOUD="powervc"
$ export CLUSTER_DIR="test"
$ export CLUSTER_NAME="rdr-xxx-openstack"
$ export FLAVOR_NAME="ocp-ipi"
$ export MACHINE_TYPE="s1122"
$ export NETWORK_NAME="vlan1337"
$ export SSHKEY_NAME="bastion-key"
$ export SERVER_IP="10.20.184.56"
```

Verify OpenStack Resources

Before proceeding, verify that your images, flavors, networks, and keypairs exist in PowerVC:

```
$ openstack --os-cloud=${CLOUD} image list
$ openstack --os-cloud=${CLOUD} flavor list
$ openstack --os-cloud=${CLOUD} network list
$ openstack --os-cloud=${CLOUD} keypair list
```

You may have to add cacert: /home/xxx/.config/openstack/powervc-ca.pem to your clouds.yaml file.

Ensure the RHCOS image is uploaded to PowerVC. If not, extract the URL from the installer and upload it:

```
$ URL=$(openshift-install coreos print-stream-json | jq -r '.architectures.ppc64le.artifacts.openstack' | jq -r '.formats."qcow2.gz".disk.location')
```

Download and upload this image to PowerVC as ${RHCOS_IMAGE_NAME}. RHCOS_IMAGE_NAME may be specified as rhcos-image, or match your enterprise rules.


```
$ openstack image create "${RHCOS_IMAGE_NAME}" \
  --container-format bare \
  --disk-format qcow2 \
  --file rhcos.qcow2
```

Step 1: Create the Bastion & Load Balancer

The IPI installation on PowerVC usually requires a bastion node to handle load balancing (HAProxy) and potentially DHCP services if they aren't provided by the infrastructure.

Generate the SSH keys for the bastion if they don't exist:

```
$ ssh-keygen
$ chmod 0600 /home/cloud-user/.ssh/id_installer_rsa
```

Run the PowerVC-Tool to create the bastion VM. This command will also set up HAProxy and configure DNS entries via IBM Cloud.

```
$ export IBMCLOUD_API_KEY="your-api-key"
$ PowerVC-Tool create-bastion \
    --cloud "${CLOUD}" \
    --bastionName "${CLUSTER_NAME}" \
    --flavorName "${FLAVOR_NAME}" \
    --imageName "${BASTION_IMAGE_NAME}" \
    --networkName "${NETWORK_NAME}" \
    --sshKeyName "${SSHKEY_NAME}" \
    --domainName "${BASEDOMAIN}" \
    --enableHAProxy true \
    --shouldDebug true
```

Wait for DNS propagation:

# Helper loop to check for api, api-int, and console DNS records

```
while true
do
        # Try the wildcard DNS entry first
        FOUND_ALL=true
        echo "Trying up to 60 times resolving *.apps.${CLUSTER_NAME}.${BASEDOMAIN} ..."
        for (( TRIES=0; TRIES<=60; TRIES++ ))
        do
                DNS="a${TRIES}.apps.${CLUSTER_NAME}.${BASEDOMAIN}"
                FOUND=false
                if getent ahostsv4 ${DNS}
                then
                        echo "Found! ${DNS}"
                        FOUND=true
                        break
                fi
                sleep 5s
        done
        if ! ${FOUND}
        then
                FOUND_ALL=false
                continue
        fi
        for PREFIX in api api-int
        do
                DNS="${PREFIX}.${CLUSTER_NAME}.${BASEDOMAIN}"
                FOUND=false
                for ((I=0; I < 10; I++))
                do
                        echo "Trying ${DNS}"
                        if getent ahostsv4 ${DNS}
                        then
                                echo "Found! ${DNS}"
                                FOUND=true
                                break
                        fi
                        sleep 5s
                done
                if ! ${FOUND}
                then
                        FOUND_ALL=false
                fi
        done
        echo "FOUND_ALL=${FOUND_ALL}"
        if ${FOUND_ALL}
        then
                break
        fi
        sleep 15s
done
```

The bastion acts as a helper node to bring up the cluster.

Step 2: Configure install-config.yaml

Generate the install-config.yaml file. This file tells the installer how to create the cluster.

Download your pull-secret from https://console.redhat.com/openshift/install/pull-secret

Copy it to ~/.pull-secret

Important: The loadBalancer type is set to UserManaged because we are using the external HAProxy set up by the PowerVC-Tool in Step 1.

```
# VIP_API=$(cat /tmp/bastionIp); VIP_INGRESS=${VIP_API}
# SUBNET_ID=$(openstack --os-cloud=${CLOUD} network show "${NETWORK_NAME}" --format shell | grep ^subnets | awk -F"'" '{print $2}')
# SSH_KEY=$(cat ~/.ssh/id_rsa.pub)
```

```
cat << ___EOF___ > ${CLUSTER_DIR}/install-config.yaml
apiVersion: v1
baseDomain: ${BASEDOMAIN}
compute:
- architecture: ppc64le
  hyperthreading: Enabled
  name: worker
  platform:
    powervc:
      zones:
        - ${MACHINE_TYPE}
  replicas: 3
controlPlane:
  architecture: ppc64le
  hyperthreading: Enabled
  name: master
  platform:
    powervc:
      zones:
        - ${MACHINE_TYPE}
  replicas: 3
metadata:
  creationTimestamp: null
  name: ${CLUSTER_NAME}
networking:
  clusterNetwork:
  - cidr: 10.116.0.0/14
    hostPrefix: 23
  machineNetwork:
  - cidr: ${MACHINE_NETWORK}
  networkType: OVNKubernetes
  serviceNetwork:
  - 172.30.0.0/16
platform:
  powervc:
    loadBalancer:
      type: UserManaged
    apiVIPs:
    - ${VIP_API}
    cloud: ${CLOUD}
    clusterOSImage: ${RHCOS_FILENAME}
    defaultMachinePlatform:
      type: ${FLAVOR_NAME}
    ingressVIPs:
    - ${VIP_INGRESS}
    controlPlanePort:
      fixedIPs:
        - subnet:
            id: ${SUBNET_ID}
credentialsMode: Passthrough
pullSecret: '${PULL_SECRET}'
sshKey: |
  ${SSH_KEY}
___EOF___
```

SSH_KEY is the public key for the private key you will use to access the cluster nodes.

MACHINE_TYPE is the Hosts > Group in PowerVC.

Verify machineNetwork.cidr this matches your SUBNET_ID CID

Note the use of architecture: ppc64le.

Step 3: Start the Watcher & Install the Cluster

Before running the install, start the watch-installation tool in the background in a clean VM. This tool acts as a bridge, detecting new VMs created by the installer and updating the bastion's HAProxy config and the local DHCP server. The watcher monitors the progress of the installation.

```
# PowerVC-Tool watch-installation \
        --cloud "${CLOUD}" \
        --domainName "${BASEDOMAIN}" \
        --bastionMetadata ${DIRECTORY} \
        --enableDhcpd true \
        ... [Additional Flags] ... &
```

DIRECTORY is the directory you are working/storing metadata about the cluster.

Run the OpenShift Installer

We will break the installation into phases to register metadata with the watcher tool before the cluster attempts to come up.

Create Configs & Manifests: This setups the materials to boot the initial cluster.

```
$ openshift-install create install-config --dir=${CLUSTER_DIR}
$ openshift-install create manifests --dir=${CLUSTER_DIR}
$ openshift-install create ignition-configs --dir=${CLUSTER_DIR}
```

Send Metadata to Watcher:

```
$ PowerVC-Tool send-metadata --createMetadata "${CLUSTER_DIR}/metadata.json" --serverIP "${SERVER_IP}"
```

Create Cluster:

```
$ openshift-install create cluster --dir=${CLUSTER_DIR} --log-level=debug
```

Step 4: Monitoring & Troubleshooting

While the cluster installs, you can monitor the progress using the watch-create command:

```
$ PowerVC-Tool watch-create \
        --metadata ${CLUSTER_DIR}/metadata.json \
        --kubeconfig ${CLUSTER_DIR}/auth/kubeconfig \
        --cloud "${CLOUD}" \
        --baseDomain "${BASEDOMAIN}" \
        --shouldDebug false
```

Once done, you can start using your cluster, or troubleshoot as you go:

Common Debugging Tips:

Check Bootstrap: Log into the PowerVC GUI, find the bootstrap VM, and open a console via the HMC connection to check for boot errors.
Check Load Balancer: Ensure the HAProxy on the bastion is running and routing traffic to the API VIP.
Check DNS: Verify that api.${CLUSTER_NAME}.${BASEDOMAIN} resolves to your bastion IP.

Here is a concise summary of the blog post, suitable for a social media snippet, newsletter intro, or meta description.

Summary

This article guides the step-by-step process for deploying Red Hat OpenShift Container Platform (OCP) on IBM PowerVC using the TechPreview of the Installer Provisioned Infrastructure (IPI) powervc method. While standard deployments can be complex, IPI PowerVC automates the provisioning of underlying resources, offering a cloud-like experience with on-premise hardware.

This approach combines the strengths of PowerVC with the automation of Installer-Provisioned Infrastrcuture to reduce deployment time and complexity.

References

IBM PowerVC Product Page
Installing OpenShift on PowerVC (Official Red Hat Docs)
PowerVC-Tool GitHub Repository
OpenShift IPI Installer Repository
