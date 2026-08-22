# PowerVC-Tool
A useful tool to create and check OpenShift clusters on IBM Cloud PowerVC.

To install an OpenShift cluster, please head to the main documentation root [here](https://github.com/IBM/ocp-ipi-powervc/tree/main/docs).

CLI commands:
- [check-alive](https://github.com/IBM/ocp-ipi-powervc/tree/main#check-alive)
- [create-bastion](https://github.com/IBM/ocp-ipi-powervc/tree/main#create-bastion)
- [create-rhcos](https://github.com/IBM/ocp-ipi-powervc/tree/main#create-rhcos)
- [delete-bastion](https://github.com/IBM/ocp-ipi-powervc/tree/main#delete-bastion)
- [erase-metadata](https://github.com/IBM/ocp-ipi-powervc/tree/main#erase-metadata)
- [rhcos-exists](https://github.com/IBM/ocp-ipi-powervc/tree/main#rhcos-exists)
- [send-metadata](https://github.com/IBM/ocp-ipi-powervc/tree/main#send-metadata)
- [watch-create](https://github.com/IBM/ocp-ipi-powervc/tree/main#watch-create)
- [watch-installation](https://github.com/IBM/ocp-ipi-powervc/tree/main#watch-installation)

## check-alive

This will check if the [controller](https://github.com/IBM/ocp-ipi-powervc/blob/main/docs/controller.md) is alive.

Example usage:
```
$ ocp-ipi-powervc-linux-amd64 check-alive --serverIP ${controller_ip} --shouldDebug false
```

args:
- `serverIP` The IP address or hostname of the controller.

- `shouldDebug` defaults to `false`.  This will cause the program to output verbose debugging information.

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

- `bastionRsa` The SSH private key file for the bastion VM.

- `availabilityZone` The name of the OpenStack availability zone (defaults to `s1022`).

- `flavorName` The OpenStack flavor to create the VM with.

- `imageName` The OpenStack image to create the VM with.

- `networkName` The OpenStack network to create the VM with.

- `sshKeyName` The OpenStack ssh keyname to create the VM with.

- `domainName` The DNS domain name for the bastion. (optional)

- `enableHAProxy` defaults to `true`.  If we should install HA Proxy on the bastion node.

- `serverIP` The IP address of the controller.

- `bastionIpFile` The filename to write the bastion IP address to (defaults to `/tmp/bastionIp`).

- `passwdHash` The password hash used in the CoreOS ignition file. (optional)

- `shouldDebug` defaults to `false`.  This will cause the program to output verbose debugging information.

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

- `availabilityZone` The name of the OpenStack availability zone (defaults to `s1022`).

- `flavorName` The OpenStack flavor to create the VM with.

- `imageName` The OpenStack image to create the VM with.

- `networkName` The OpenStack network to create the VM with.

- `passwdHash` The password hash of the core user.

- `sshPublicKey` The SSH public key contents to inject into the VM.

- `domainName` The DNS domain name for the VM. (optional)

- `timeout` Maximum duration for the operation (defaults to `15m`).

- `shouldDebug` defaults to `false`.  This will cause the program to output verbose debugging information.

## delete-bastion

This will delete an existing bastion HAProxy VM.

Example usage:

```
$ ocp-ipi-powervc-linux-amd64 delete-bastion --cloud ${cloud_name} --bastionName ${bastion_name} --shouldDebug true
```

args:
- `cloud` the name of the cloud to use in the `~/.config/openstack/clouds.yaml` file.

- `bastionName` The name of the bastion VM to delete.

- `shouldDebug` defaults to `false`.  This will cause the program to output verbose debugging information.

## erase-metadata

This will erase cluster metadata entries matching a pattern from a remote server.

Example usage:

```
$ ocp-ipi-powervc-linux-amd64 erase-metadata --pattern "test-*" --serverIP ${controller_ip} --timeout 5m --shouldDebug true
```

args:
- `pattern` Pattern to match metadata entries for deletion (e.g., `test-*`, `staging-*`).

- `serverIP` The IP address of the controller.

- `timeout` Timeout for the erase operation (defaults to `1m`, e.g., `5m`, `10m`, `30s`).

- `shouldDebug` defaults to `false`.  This will cause the program to output verbose debugging information.

## rhcos-exists

This will verify that a named RHCOS image exists in the OpenStack cloud.  If the image is not found, all available images are listed to help diagnose naming issues.

Example usage:

```
$ ocp-ipi-powervc-linux-amd64 rhcos-exists --cloud ${cloud_name} --imageName ${image_name} --shouldDebug false
```

args:
- `cloud` the name of the cloud to use in the `~/.config/openstack/clouds.yaml` file.

- `imageName` The name of the RHCOS image to search for (case-sensitive, exact match).

- `shouldDebug` defaults to `false`.  This will cause the program to output verbose debugging information.

## send-metadata

This will send a command to the server to either create or delete a local copy of the metadata.json file.

Example usage:

```
$ ocp-ipi-powervc-linux-amd64 send-metadata --createMetadata ${directory}/metadata.json --serverIP ${controller_ip} --shouldDebug true
```

args:

- `createMetadata` Tells the server to create a local copy of this metadata.json file (mutually exclusive with `deleteMetadata`).

- `deleteMetadata` Tells the server to delete a local copy of this metadata.json file (mutually exclusive with `createMetadata`).

- `serverIP` The IP address of the controller.

- `timeout` Timeout for the send operation (defaults to `1m`, e.g., `5m`, `10m`, `30s`).

- `shouldDebug` defaults to `false`.  This will cause the program to output verbose debugging information.

## watch-create

This monitors and displays the status of cluster resources during and after cluster creation.  It queries the state of VMs, the load balancer, and optionally the OpenShift cluster and IBM DNS.

NOTE:
The environment variable `IBMCLOUD_API_KEY` needs to be set.

Example usage:

```
$ ocp-ipi-powervc-linux-amd64 watch-create --metadata ${directory}/metadata.json --kubeconfig ${directory}/auth/kubeconfig --cloud ${cloud_name} --bastionRsa ${HOME}/.ssh/id_installer_rsa --baseDomain ${domain_name} --shouldDebug false
```

args:
- `cloud` the name of the cloud to use in the `~/.config/openstack/clouds.yaml` file.

- `metadata` the location of the `metadata.json` file created by the IPI OpenShift installer.

- `kubeconfig` the location of the `kubeconfig` file created by the IPI OpenShift installer. (optional)

- `bastionRsa` the SSH private key file for the bastion VM.

- `baseDomain` the domain name of the OpenShift cluster. (optional)

- `shouldDebug` defaults to `false`.  This will cause the program to output verbose debugging information.

## watch-installation

This is for checking the progress of an ongoing `openshift-install create cluster` operation of the OpenShift IPI installer.  Run this in another window while the installer deploys a cluster.

NOTE:
The environment variable `IBMCLOUD_API_KEY` is optional.  If not set, make sure DNS is supported via CoreOS DNS or another method.

Example usage:

```
$ ocp-ipi-powervc-linux-amd64 watch-installation --cloud ${cloud_name} --domainName ${domain_name} --bastionMetadata ${directory}/metadata.json --bastionRsa ${HOME}/.ssh/id_installer_rsa --dhcpSubnet ${dhcp_subnet} --dhcpNetmask ${dhcp_netmask} --dhcpRouter ${dhcp_router} --dhcpDnsServers "${dhcp_servers}" --shouldDebug true
```

args:
- `cloud` the name of the cloud to use in the `~/.config/openstack/clouds.yaml` file.

- `domainName` the domain name to use for the OpenShift cluster.

- `bastionMetadata` the location of the `metadata.json` file created by the IPI OpenShift installer.  This parameter can have more than one occurrence.

- `bastionRsa` the SSH private key file for the default username for the HAProxy VM.

- `enableDhcpd` defaults to `false`.  Enables updating the locally installed DHCP server.

- `dhcpInterface` The network interface to listen for DHCPd requests.

- `dhcpSubnet` The subnet to use for DHCPd requests.

- `dhcpNetmask` The netmask to use for DHCPd requests.

- `dhcpRouter` The router to use for DHCPd requests.

- `dhcpDnsServers` The comma separated DNS servers to use for DHCPd requests.

- `dhcpServerId` The DNS server identifier for a DHCP request.

- `statsUser` HAProxy stats username (leave empty to disable stats). (optional)

- `statsPassword` HAProxy stats password. (optional)

- `shouldDebug` defaults to `false`.  This will cause the program to output verbose debugging information.

# Useful scripts

## scripts/build-nightly.sh

This script downloads a nightly OCP build and sets up the environment for testing.

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

## scripts/cleanup-containers.sh

This script removes all containers and their objects from an OpenStack cloud.  It processes containers one at a time, deleting all objects before removing the container itself.  Optionally filters by infrastructure ID.

Required environment variables before running this script:

- `CLOUD` the OpenStack cloud name from `clouds.yaml`.

Optional arguments:

- `INFRA_ID` filter containers by infrastructure ID (e.g., `cluster-abc123`).

Required existing binaries before running this script:

- `openstack` The OpenStack CLI tool.

## scripts/console.sh

This script will output the ssh command needed to access the console for a VM or OpenShift node name.

## scripts/create-cluster.sh

This script will create an OpenShift cluster using the IPI installer.

Required environment variables before running this script:

- `BASEDOMAIN` the domain name to use for the OpenShift cluster.

- `BASTION_IMAGE_NAME` the OpenStack image name for the HAProxy VM.

- `BASTION_RSA` the ssh private key for the bastion node. (used for failure diagnostics)

- `CLOUD` the name of the cloud to use in the `~/.config/openstack/clouds.yaml` file.

- `CLUSTER_DIR` the directory location where the OpenShift IPI installer will save important files. (defaults to `test`)

- `CLUSTER_NAME` the name prefix to use for the OpenShift cluster which you are installing.

- `CONTROLLER_IP` the IP address of the controller.

- `FLAVOR_NAME` the OpenStack flavor name to use for OpenShift VMs.

- `INSTALLER_SSHKEY` the path to the ssh public key for access to the bootstrap and master nodes.  Usually named `~/.ssh/id_installer_rsa.pub`.

- `MACHINE_TYPE` the PowerPC machine type / availability zone to use for OpenShift VMs.

- `NETWORK_NAME` the OpenStack network name to use for OpenShift VMs.

- `PROJECT` an optional prefix to prepend to the RHCOS image name. (optional)

- `PULL_SECRET` the pull secret content (used inline in install-config). (optional alternative to `PULLSECRET_FILE`)

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

- `getent` DNS resolution utility.

- `podman` Container tool used for pull secret validation.

- `ssh-keygen` Used for optional SSH public key validation and controller connectivity check.

- `ping` Used to verify controller connectivity.

## scripts/list-bastions.sh

This script lists bastion (standalone) VMs on PowerVC/OpenStack.  Any server whose name does not match the cluster-node pattern is treated as a bastion VM.

Optional arguments:

- `--cloud <cloud>` OpenStack cloud name (overrides `$CLOUD` / `$OS_CLOUD`).

- `--bastionRSA <path>` Path to SSH private key for bastion access.  When provided, each listed bastion is probed over SSH and only reachable ones are shown (with the matching username).

Optional environment variables:

- `CLOUD` the OpenStack cloud name from `clouds.yaml` (skips interactive prompt if set).

- `BASTION_RSA` path to SSH private key for bastion access (skips prompt if set).

Required existing binaries before running this script:

- `openstack` The OpenStack CLI tool.

- `ssh` Required only when `--bastionRSA` is provided.

## scripts/current-servers.sh

This script lists OpenStack servers grouped by cluster and standalone VMs.

Optional arguments:

- `-c <cloud>` OpenStack cloud name (overrides `$CLOUD` / `$OS_CLOUD`).

Required existing binaries before running this script:

- `openstack` The OpenStack CLI tool.

## scripts/delete-cluster.sh

This script will delete an OpenShift cluster using the IPI installer.

Required environment variables before running this script:

- `CLUSTER_DIR` the directory location where the OpenShift IPI installer will save important files.

- `CONTROLLER_IP` the IP address of the controller.

Required existing binaries before running this script:

- `openshift-install` The OpenShift IPI installer.

- `ocp-ipi-powervc-linux-${ARCH}` This repo tool.

- `ping` a Linux admin tool.

## scripts/list-dns.sh

This script lists DNS records from an IBM Cloud CIS (Cloud Internet Services) instance.  It interactively selects the CIS instance and domain, then paginates through all DNS records, optionally filtering by a regex pattern.

Required environment variables before running this script:

- `IBMCLOUD_API_KEY` IBM Cloud API key used for authentication.

- `BASEDOMAIN` DNS base domain; used to auto-select the matching CIS domain when only one matches.

Optional arguments:

- `-m, --match <pattern>` Filter DNS records by name (regex).

- `-r, --region <region>` IBM Cloud region to target (defaults to `us-south`).

Required existing binaries before running this script:

- `ibmcloud` IBM Cloud CLI with the CIS plugin installed.

- `jq` The JSON query CLI tool.

## scripts/list-clusters-and-delete.sh

This script lists running OpenShift clusters on PowerVC/OpenStack, prompts the user to select one, and deletes it.

Optional arguments:

- `-c <cloud>` OpenStack cloud name (skips interactive prompt).

- `-l` List clusters only; do not prompt for deletion.

Optional environment variables:

- `CLOUD` the OpenStack cloud name from `clouds.yaml` (skips interactive prompt if set).

Required existing binaries before running this script:

- `openstack` The OpenStack CLI tool.

- `openshift-install` The OpenShift IPI installer.

## scripts/print-stream-json.sh

This script downloads CoreOS JSON metadata and verifies that RHCOS images exist in OpenStack.  It supports multiple release versions, multiple output formats (text, JSON, CSV), dry-run mode, and RHEL version preference (RHEL 9 or RHEL 10).

Required existing binaries before running this script:

- `openstack` The OpenStack CLI tool.

- `jq` The JSON query CLI tool.

- `curl` For downloading CoreOS metadata.

## scripts/rename-images.sh

This script bulk-renames RHCOS images in OpenStack by adding a project prefix to their names.

Required existing binaries before running this script:

- `openstack` The OpenStack CLI tool.

## scripts/ssh.sh

This script will output the ssh command needed to access a specific OpenShift node.

## scripts/upload-centos.sh

This script downloads and uploads CentOS Stream GenericCloud images to PowerVC/OpenStack.  It resolves the latest dated `.qcow2` image for either CentOS Stream 9 or CentOS Stream 10 (ppc64le) from the official CentOS cloud mirror, verifies its SHA1 checksum, derives the image name, checks whether the image already exists in OpenStack, and if not converts it with `pvsadm` and imports it via `pvcctl` or `powervc-image`.

Required environment variables before running this script:

- `CLOUD` OpenStack cloud name from `~/.config/openstack/clouds.yaml` (or `--cloud`).

- `PROJECT_UPLOAD` PowerVC project to associate the uploaded image with (or `--project-upload`).

- `CENTOS_VERSION` CentOS stream version: `CentOS9` or `CentOS10` (or `--centOS`).

- `SVC_HOST` PowerVC service host used by `pvcctl` (or `--svc-host`).

- `TEMPLATE` PowerVC template UUID used during image import (or `--template`).

Required existing binaries before running this script:

- `curl` For downloading files and checking URLs.

- `jq` The JSON query CLI tool.

- `openstack` The OpenStack CLI tool.

- `pvsadm` For converting qcow2 images to OVA format.

- `pvcctl` or `powervc-image` For importing images into PowerVC (either one required).

Example usage:

```bash
source environmentCI
./scripts/upload-centos.sh --centOS CentOS9
./scripts/upload-centos.sh --centOS CentOS10 --dry-run
./scripts/upload-centos.sh --centOS CentOS9 --verbose
```

## scripts/upload-rhcos.sh

This script downloads and uploads RHCOS images to PowerVC/OpenStack.  It handles multiple release versions, supports both RHEL 9 and RHEL 10 based images, converts images with `pvsadm`, and imports them via `pvcctl` or `powervc-image`.

Required existing binaries before running this script:

- `curl` For downloading files and checking URLs.

- `jq` The JSON query CLI tool.

- `openstack` The OpenStack CLI tool.

- `pvsadm` For converting qcow2 images to OVA format.

- `pvcctl` or `powervc-image` For importing images into PowerVC (either one required).

## scripts/wait-for-dns.sh

This script polls DNS servers to verify that all required DNS entries for an OpenShift cluster are resolvable before proceeding with installation.  It checks wildcard DNS entries (`*.apps`), the API endpoint, and the internal API endpoint.

Required environment variables before running this script:

- `CLUSTER_DIR` directory containing cluster metadata (prompts if not set).

- `BASEDOMAIN` base domain for the cluster (prompts if not set).

Required existing files before running this script:

- `${CLUSTER_DIR}/metadata.json` Cluster metadata containing the cluster name.

Required existing binaries before running this script:

- `jq` The JSON query CLI tool.

- `getent` DNS resolution utility.

# Companion tools

Standalone Go binaries that live in sub-directories and are built/installed separately from the main `ocp-ipi-powervc` binary.

- [JobHistory](#jobhistory)
- [UploadCentos](#uploadcentos)
- [UploadRhcos](#uploadrhcos)

## JobHistory

Extracts CI run information from OpenShift Prow job history pages and reports build and test results.  It can filter by date, output CSV files, and process multiple job URLs in one run.

Build:

```bash
make build-jobhistory
```

**Important**: All flags must be specified **before** the URL(s).

Example usage:

```bash
# All runs today, CSV output
./JobHistory/JobHistory --today --csv --output output.csv \
  https://prow.ci.openshift.org/job-history/gs/test-platform-results/logs/<job-name>/

# Last 7 days
./JobHistory/JobHistory --last-n-days 7 <URL>

# Custom date range
./JobHistory/JobHistory --after-date 2024-07-01 --before-date 2024-07-31 <URL>
```

args:
- `--today` Only show runs from today.

- `--yesterday` Only show runs from yesterday.

- `-l, --last-n-days <n>` Only show runs from the last n days.

- `-a, --after-date <date>` Only show runs after this date (ISO 8601, e.g. `2024-07-01`).

- `-b, --before-date <date>` Only show runs before this date (ISO 8601).

- `-c, --csv` Output in CSV format.

- `-o, --output <file>` Write output to this file instead of stdout.

- `-d, --deploy-status-only` Only show deploy failures.

- `-t, --test-status-only` Only show test failures.

- `-v, --version` Display the program version.

## UploadCentos

Downloads the latest CentOS Stream GenericCloud `ppc64le` image from `cloud.centos.org` and uploads it into a PowerVC/OpenStack environment.  It checks whether the image already exists before uploading and optionally converts to OVA format with `pvsadm`.

Build:

```bash
make build-uploadcentos
```

Example usage:

```bash
# Upload CentOS Stream 9
./UploadCentos/UploadCentos \
  --cloud mycloud \
  --project-upload myproject \
  --centos CentOS9 \
  --svc-host powervc.example.com \
  --template xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx

# Dry run
./UploadCentos/UploadCentos --centos CentOS9 --dry-run
```

args / environment variables:
- `--cloud` / `CLOUD` OpenStack cloud name from `clouds.yaml` (required).

- `--centos` / `CENTOS_VERSION` CentOS Stream version: `CentOS9` or `CentOS10` (required).

- `--project-upload` / `PROJECT_UPLOAD` PowerVC project to associate the uploaded image with (required).

- `--svc-host` / `SVC_HOST` PowerVC service host used by `pvcctl` (required).

- `--template` / `TEMPLATE` PowerVC template UUID used during image import (required).

- `--project` / `PROJECT` Optional prefix prepended to image filenames.

- `--date <YYYYMMDD>` Pin to a specific image build date instead of using the latest.

- `--dry-run` Print commands without executing them.

- `-v, --verbose` Enable debug output.

- `-q, --quiet` Suppress all non-error output.

Required existing binaries:

- `openstack` The OpenStack CLI tool.

- `pvcctl` or `powervc-image` For importing images into PowerVC (either one required).

- `pvsadm` For converting qcow2 images to OVA format (optional; required when using `powervc-image`).

## UploadRhcos

Downloads Red Hat CoreOS (RHCOS) `ppc64le` images from the OpenShift installer GitHub repository and uploads them into a PowerVC/OpenStack environment.  Supports multiple release branches, RHEL 9 and RHEL 10 based images, and optional OVA conversion with `pvsadm`.

Build:

```bash
make build-uploadrhcos
```

Example usage:

```bash
# Upload a single release
./UploadRhcos/UploadRhcos \
  --cloud mycloud \
  --project-upload myproject \
  --release release-4.21 \
  --rhel rhel9 \
  --svc-host powervc.example.com \
  --template xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx

# Multiple releases, dry run
./UploadRhcos/UploadRhcos --release release-4.21 --release release-4.22 --dry-run
```

args / environment variables:
- `--cloud` / `CLOUD` OpenStack cloud name from `clouds.yaml` (required).

- `--project-upload` / `PROJECT_UPLOAD` PowerVC project to associate the uploaded image with (required).

- `--release <version>` Release branch to process (e.g. `release-4.21`); may be repeated for multiple releases (defaults to `release-4.21`).

- `--rhel` / `RHEL_VERSION` RHEL version preference for CoreOS JSON selection: `rhel9` or `rhel10`. (optional)

- `--svc-host` / `SVC_HOST` PowerVC service host used by `pvcctl` (required).

- `--template` / `TEMPLATE` PowerVC template UUID used during image import (required).

- `--project` / `PROJECT` Optional prefix prepended to image filenames.

- `--dry-run` Print commands without executing them.

- `-v, --verbose` Enable debug output.

- `--quiet` Suppress all non-error output.

Required existing binaries:

- `openstack` The OpenStack CLI tool.

- `pvcctl` or `powervc-image` For importing images into PowerVC (either one required).

- `pvsadm` For converting qcow2 images to OVA format (optional; required when using `powervc-image`).

