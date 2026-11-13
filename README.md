# ocp-ipi-powervc

A CLI tool and companion script suite for deploying and managing OpenShift clusters on IBM PowerVC using the IPI (Installer-Provisioned Infrastructure) installer.

The tool coordinates the OpenShift IPI installer, PowerVC/OpenStack infrastructure, an HAProxy bastion load balancer, and optional IBM Cloud DNS into a single automated workflow.

---

## Table of Contents

- [Overview](#overview)
- [Prerequisites](#prerequisites)
- [Building](#building)
- [CLI Commands](#cli-commands)
  - [check-alive](#check-alive)
  - [create-bastion](#create-bastion)
  - [create-rhcos](#create-rhcos)
  - [delete-bastion](#delete-bastion)
  - [erase-metadata](#erase-metadata)
  - [rhcos-exists](#rhcos-exists)
  - [send-metadata](#send-metadata)
  - [watch-create](#watch-create)
  - [watch-installation](#watch-installation)
- [Scripts](#scripts)
  - [create-cluster.sh configuration](#create-clustersh-configuration)
- [Companion Tools](#companion-tools)
- [CI and Release](#ci-and-release)
- [Documentation](#documentation)
- [License](#license)

---

## Overview

`ocp-ipi-powervc` operates in a client-server model. A **controller** process runs on a dedicated VM and manages cluster lifecycle; CLI commands are dispatched to it (or run locally) to create bastions, send metadata, and monitor progress. See [docs/controller.md](docs/controller.md) for controller setup.

The full installation documentation index is at [docs/README.md](docs/README.md).

---

## Prerequisites

| Requirement | Notes |
|---|---|
| PowerVC server | Accessible over the network |
| `~/.config/openstack/clouds.yaml` | PowerVC credentials — see [docs/configure-openstack.md](docs/configure-openstack.md) |
| OpenShift pull secret | Download from [console.redhat.com](https://console.redhat.com/openshift/install/pull-secret), save as `~/.pullSecretCompact` |
| SSH installer key | `ssh-keygen -t rsa -b 4096 -f ~/.ssh/id_installer_rsa` |
| `openshift-install` binary | See [docs/IPI-installer.md](docs/IPI-installer.md) |
| RHCOS image in PowerVC | See [docs/IPI-installer.md](docs/IPI-installer.md) |
| Controller VM | Running this tool's server mode — see [docs/controller.md](docs/controller.md) |
| Environment variables | See [docs/environment-variables.md](docs/environment-variables.md) |

Required external binaries: `openstack`, `jq`, `getent`, `podman`, `ssh-keygen`, `ping`.

---

## Building

```bash
# Build for the current platform
make build

# Build all tools (main binary + all companion tools)
make build-all

# Build distribution binaries for all supported platforms (linux/darwin × amd64/arm64/ppc64le)
make dist

# Quick development cycle (deps → build → test)
make dev

# Run tests
make test

# Format, vet and test
make check

# Run golangci-lint (requires golangci-lint)
make lint

# Prepare a release (clean, deps, test, dist, release-scripts)
make release

# Show all Makefile targets
make help
```

Other useful targets: `test-race`, `test-coverage`, `coverage-html`, `bench`, `fmt`, `vet`, `tidy`, `checksums`.

Each companion tool has its own `init-<tool>`, `build-<tool>`, `install-<tool>` and `dist-<tool>` targets.

The output binary is named `ocp-ipi-powervc-<os>-<arch>` (e.g. `ocp-ipi-powervc-linux-x86_64`). The command examples below use `ocp-ipi-powervc-linux-amd64`; substitute the binary name for your platform.

> **Note:** The `create-cluster` command implementation (`CmdCreateCluster.go`, `CreateClusterPhase*.go`) is currently disabled and is not available from the CLI. Use [`scripts/create-cluster.sh`](scripts/create-cluster.sh) instead.

---

## CLI Commands

```
ocp-ipi-powervc-linux-<arch> <command> [flags]
ocp-ipi-powervc-linux-<arch> --version
ocp-ipi-powervc-linux-<arch> --help
```

Exit codes: `0` success · `2` invalid args · `3` unknown command · `4` command failed.

---

### check-alive

Checks whether the controller service is reachable.

```bash
ocp-ipi-powervc-linux-amd64 check-alive --serverIP ${controller_ip}
```

| Flag | Description |
|---|---|
| `--serverIP` | IP address or hostname of the controller |
| `--shouldDebug` | Verbose debug output (default: `false`) |

---

### create-bastion

Creates an HAProxy VM that acts as an OpenShift load balancer. The bastion is managed by a `watch-installation` process.

> **Note:** `IBMCLOUD_API_KEY` is optional. If not set, ensure DNS is provided by CoreDNS or another method.

```bash
ocp-ipi-powervc-linux-amd64 create-bastion \
  --cloud ${cloud_name} \
  --bastionName ${bastion_name} \
  --flavorName ${flavor_name} \
  --imageName ${image_name} \
  --networkName ${network_name} \
  --sshKeyName ${ssh_keyname} \
  --serverIP ${controller_ip} \
  --shouldDebug true
```

| Flag | Description |
|---|---|
| `--cloud` | Cloud name from `~/.config/openstack/clouds.yaml` |
| `--bastionName` | VM name (should match the OpenShift cluster name) |
| `--bastionRsa` | SSH private key file for the bastion VM |
| `--availabilityZone` | OpenStack availability zone (default: `s1022`) |
| `--flavorName` | OpenStack flavor for the VM |
| `--imageName` | OpenStack image for the VM |
| `--networkName` | OpenStack network for the VM |
| `--sshKeyName` | OpenStack SSH key name for the VM |
| `--domainName` | DNS domain name for the bastion (optional) |
| `--enableHAProxy` | Install HAProxy on the bastion (default: `true`) |
| `--serverIP` | IP address of the controller |
| `--bastionIpFile` | File to write the bastion IP into (default: `/tmp/bastionIp`) |
| `--passwdHash` | Password hash used in the CoreOS ignition file (optional) |
| `--shouldDebug` | Verbose debug output (default: `false`) |

---

### create-rhcos

Creates a test RHCOS VM managed by the controller.

> **Note:** `IBMCLOUD_API_KEY` is optional. If not set, ensure DNS is handled externally.

```bash
ocp-ipi-powervc-linux-amd64 create-rhcos \
  --cloud ${cloud_name} \
  --rhcosName ${rhcos_name} \
  --flavorName ${flavor_name} \
  --imageName ${image_name} \
  --networkName ${network_name} \
  --sshPublicKey $(cat ~/.ssh/id_installer_rsa.pub) \
  --shouldDebug true
```

| Flag | Description |
|---|---|
| `--cloud` | Cloud name from `~/.config/openstack/clouds.yaml` |
| `--rhcosName` | VM name (should match the OpenShift cluster name) |
| `--availabilityZone` | OpenStack availability zone (default: `s1022`) |
| `--flavorName` | OpenStack flavor for the VM |
| `--imageName` | OpenStack image for the VM |
| `--networkName` | OpenStack network for the VM |
| `--passwdHash` | Password hash of the core user |
| `--sshPublicKey` | SSH public key contents to inject into the VM |
| `--domainName` | DNS domain name for the VM (optional) |
| `--timeout` | Maximum operation duration (default: `15m`) |
| `--shouldDebug` | Verbose debug output (default: `false`) |

---

### delete-bastion

Deletes an existing bastion HAProxy VM.

```bash
ocp-ipi-powervc-linux-amd64 delete-bastion \
  --cloud ${cloud_name} \
  --bastionName ${bastion_name} \
  --shouldDebug true
```

| Flag | Description |
|---|---|
| `--cloud` | Cloud name from `~/.config/openstack/clouds.yaml` |
| `--bastionName` | Name of the bastion VM to delete |
| `--shouldDebug` | Verbose debug output (default: `false`) |

---

### erase-metadata

Erases cluster metadata entries matching a pattern from the controller.

```bash
ocp-ipi-powervc-linux-amd64 erase-metadata \
  --pattern "test-*" \
  --serverIP ${controller_ip} \
  --timeout 5m
```

| Flag | Description |
|---|---|
| `--pattern` | Pattern to match metadata entries for deletion (e.g. `test-*`) |
| `--serverIP` | IP address of the controller |
| `--timeout` | Operation timeout (default: `1m`) |
| `--shouldDebug` | Verbose debug output (default: `false`) |

---

### rhcos-exists

Verifies that a named RHCOS image exists in the OpenStack cloud. Lists all available images if not found.

```bash
ocp-ipi-powervc-linux-amd64 rhcos-exists \
  --cloud ${cloud_name} \
  --imageName ${image_name}
```

| Flag | Description |
|---|---|
| `--cloud` | Cloud name from `~/.config/openstack/clouds.yaml` |
| `--imageName` | RHCOS image name to search for (case-sensitive, exact match) |
| `--shouldDebug` | Verbose debug output (default: `false`) |

---

### send-metadata

Sends a command to the controller to create or delete a local copy of `metadata.json`.

```bash
ocp-ipi-powervc-linux-amd64 send-metadata \
  --createMetadata ${cluster_dir}/metadata.json \
  --serverIP ${controller_ip}
```

| Flag | Description |
|---|---|
| `--createMetadata` | Tells the controller to create a copy of this metadata.json (mutually exclusive with `--deleteMetadata`) |
| `--deleteMetadata` | Tells the controller to delete a copy of this metadata.json (mutually exclusive with `--createMetadata`) |
| `--serverIP` | IP address of the controller |
| `--timeout` | Operation timeout (default: `1m`) |
| `--shouldDebug` | Verbose debug output (default: `false`) |

---

### watch-create

Monitors and displays the status of cluster resources during and after cluster creation — VMs, the load balancer, and optionally the OpenShift cluster and IBM DNS.

> **Note:** `IBMCLOUD_API_KEY` must be set.

```bash
ocp-ipi-powervc-linux-amd64 watch-create \
  --metadata ${cluster_dir}/metadata.json \
  --kubeconfig ${cluster_dir}/auth/kubeconfig \
  --cloud ${cloud_name} \
  --bastionRsa ~/.ssh/id_installer_rsa \
  --baseDomain ${domain_name}
```

| Flag | Description |
|---|---|
| `--cloud` | Cloud name from `~/.config/openstack/clouds.yaml` |
| `--metadata` | Path to `metadata.json` created by the IPI installer |
| `--kubeconfig` | Path to `kubeconfig` created by the IPI installer (optional) |
| `--bastionRsa` | SSH private key file for the bastion VM |
| `--baseDomain` | Domain name of the OpenShift cluster (optional) |
| `--shouldDebug` | Verbose debug output (default: `false`) |

---

### watch-installation

Monitors the progress of an active `openshift-install create cluster` operation. Run this in a separate window while the installer deploys.

> **Note:** `IBMCLOUD_API_KEY` is optional. If not set, ensure DNS is handled externally.

```bash
ocp-ipi-powervc-linux-amd64 watch-installation \
  --cloud ${cloud_name} \
  --domainName ${domain_name} \
  --bastionMetadata ${installs_dir} \
  --bastionRsa ~/.ssh/id_installer_rsa \
  --dhcpSubnet ${dhcp_subnet} \
  --dhcpNetmask ${dhcp_netmask} \
  --dhcpRouter ${dhcp_router} \
  --dhcpDnsServers "${dhcp_servers}"
```

| Flag | Description |
|---|---|
| `--cloud` | Cloud name from `~/.config/openstack/clouds.yaml`; may be specified more than once |
| `--domainName` | Domain name for the OpenShift cluster |
| `--bastionMetadata` | Root directory containing the OpenShift cluster install directories (one per infra ID, each holding a `metadata.json`) |
| `--bastionRsa` | SSH private key for the HAProxy VM |
| `--enableDhcpd` | Enable updating the local DHCP server (default: `false`) |
| `--dhcpServer` | DHCP server to manage: `dhcpd` or `kea` (default: `dhcpd`) |
| `--dhcpInterface` | Network interface for DHCPd requests |
| `--dhcpSubnet` | Subnet for DHCPd requests |
| `--dhcpNetmask` | Netmask for DHCPd requests |
| `--dhcpRouter` | Router for DHCPd requests |
| `--dhcpDnsServers` | Comma-separated DNS servers for DHCPd requests |
| `--dhcpServerId` | DHCP server identifier |
| `--statsUser` | HAProxy stats username — leave empty to disable (optional) |
| `--statsPassword` | HAProxy stats password (optional) |
| `--log` | File to log all output to |
| `--shouldDebug` | Verbose debug output (default: `false`) |

---

## Scripts

Helper shell scripts live in the [`scripts/`](scripts/) directory and are included in release tarballs.

| Script | Purpose |
|---|---|
| `create-cluster.sh` | Create an OpenShift cluster using the IPI installer |
| `delete-cluster.sh` | Delete an OpenShift cluster |
| `check-alive.sh` | Check the controller is running; start it in tmux if not |
| `console.sh` | Print the SSH command to access a VM or node by name |
| `ssh.sh` | Print the SSH command to access a specific OpenShift node |
| `current-servers.sh` | List OpenStack servers grouped by cluster |
| `list-bastions.sh` | List standalone bastion VMs; optionally probe over SSH |
| `list-clusters-and-delete.sh` | List clusters and interactively delete one |
| `list-dns.sh` | List DNS records from an IBM Cloud CIS instance |
| `list-openstack-versions.sh` | List OCP release versions that have ppc64le builds |
| `print-stream-json.sh` | Download CoreOS metadata and verify RHCOS images exist in OpenStack |
| `upload-rhcos.sh` | Download and upload RHCOS images to PowerVC |
| `upload-centos.sh` | Download and upload CentOS Stream images to PowerVC |
| `watch-rhcos-images.sh` | Watch the ppc64le OCP CI release streams and upload new RHCOS images to PowerVC every 24 hours |
| `rename-images.sh` | Bulk-rename RHCOS images in OpenStack by adding a project prefix |
| `cleanup-containers.sh` | Remove all OpenStack containers and their objects |
| `find-orphaned-ports.py` | Find (and optionally delete with `--delete`) orphaned Neutron ports in the current project |
| `watch-bastions-port.sh` | Continuously watch bastion VMs and print a line when their addresses or ports change or a bastion disappears |
| `wait-for-dns.sh` | Poll DNS until all required OpenShift cluster entries are resolvable |
| `build-nightly.sh` | Download a nightly OCP build and set up a test environment |
| `extract-release-tools.sh` | Extract the `openshift-install` and `oc` binaries for a release into `./<VERSION>/` |
| `download-yq.sh` | Download and checksum-verify a `mikefarah/yq` release |
| `fetch-cluster-artifacts.sh` | Fetch cluster artifacts from a CI pod and the controller host |
| `sriov-worker-watch.sh` | Provision SR-IOV-enabled worker nodes one at a time |
| `delete-pods-e2e-pod-network-disruption-test.sh` | Delete leftover `e2e-pod-network-disruption-test-*` namespaces (pattern is the first argument) |

See each script's inline comments for full flag details.

### create-cluster.sh configuration

`create-cluster.sh` reads its settings from environment variables, prompting for any required value that is not set. Ready-made examples live in the top-level `environment*` files (e.g. `environmentSelfSigned`); see [docs/environment-variables.md](docs/environment-variables.md) for the full list.

| Variable | Description |
|---|---|
| `CLOUD`, `BASEDOMAIN`, `CLUSTER_NAME`, `CLUSTER_DIR`, `NETWORK_NAME`, `CONTROLLER_IP` | Cloud, DNS domain, cluster name and directory, OpenStack network, and PowerVC controller IP |
| `BASTION_IMAGE_NAME`, `SSHKEY_NAME`, `BASTION_RSA` | Bastion image, OpenStack keypair, and private SSH key |
| `PULL_SECRET` / `PULLSECRET_FILE`, `INSTALLER_SSHKEY` | Pull secret (content or file) and installer public SSH key |
| `FLAVOR_NAME` | Flavor for cluster nodes (`defaultMachinePlatform`) |
| `BASTION_FLAVOR_NAME`, `CONTROL_FLAVOR_NAME`, `COMPUTE_FLAVOR_NAME` | Optional per-role flavors (default: `FLAVOR_NAME`) |
| `MACHINE_TYPE` | Availability zone for the bastion and the default for the control and compute planes |
| `CONTROL_MACHINE_TYPE`, `COMPUTE_MACHINE_TYPE` | Optional per-plane availability zones (default: `MACHINE_TYPE`) |
| `CONTROL_REPLICAS`, `COMPUTE_REPLICAS` | Optional node counts (default: `3` each) |

---

## Companion Tools

Standalone Go binaries in sub-directories, built and installed separately from the main binary. `make build-all` builds all of them.

### GrabConsole

Captures serial console output from a PowerVC/OpenStack VM over SSH.

```bash
make build-grabconsole
./GrabConsole/GrabConsole --help
```

### SshNode

Establishes an SSH connection to a specific OpenShift node by name.

```bash
make build-sshnode
./SshNode/SshNode --help
```

### JobHistory

Extracts CI run information from OpenShift Prow job history pages and reports build and test results. Supports date filtering and CSV output.

```bash
make build-jobhistory

# All runs today, CSV output
./JobHistory/JobHistory --today --csv --output output.csv \
  https://prow.ci.openshift.org/job-history/gs/test-platform-results/logs/<job-name>/

# Last 7 days
./JobHistory/JobHistory --last-n-days 7 <URL>
```

| Flag | Description |
|---|---|
| `--today` | Only show runs from today |
| `--yesterday` | Only show runs from yesterday |
| `-l, --last-n-days <n>` | Only show runs from the last n days |
| `-a, --after-date <date>` | Only show runs after this date (ISO 8601) |
| `-b, --before-date <date>` | Only show runs before this date (ISO 8601) |
| `-c, --csv` | Output in CSV format |
| `-o, --output <file>` | Write output to a file instead of stdout |
| `-d, --deploy-status-only` | Only show deploy failures |
| `-t, --test-status-only` | Only show test failures |

### UploadRhcosAPI

Downloads RHCOS `ppc64le` images and uploads them into PowerVC/OpenStack by calling the PowerVC image import service directly, instead of running `pvcctl` as `UploadRhcos` does. It reads sensitive settings from environment variables and supports self-signed PowerVC certificates. See [UploadRhcosAPI/README.md](UploadRhcosAPI/README.md) for the full list of environment variables.

```bash
make build-uploadrhcosapi

./UploadRhcosAPI/UploadRhcosAPI --cloud mycloud --release release-4.21 --rhel rhel9
```

| Flag | Description |
|---|---|
| `--cloud` | OpenStack cloud name from `clouds.yaml` |
| `--release <version>` | Release branch; may be repeated (default: `release-4.21`) |
| `--rhel` | `rhel9` or `rhel10` |
| `--ca-cert <path>` | PEM CA certificate for the import service (auto-detected from `clouds.yaml` when `--cloud` is set) |
| `--insecure` | Skip TLS certificate verification for the import service |
| `--connect-timeout <d>` | Timeout for the OpenStack connectivity check (default: `2m`) |
| `--dry-run` | Simulate operations without executing them |
| `--log-payload` | Print the outgoing import JSON payload (credentials redacted) |
| `-v, --verbose` | Enable debug output |
| `--quiet` | Suppress non-error output |

### UploadCentos

Downloads the latest CentOS Stream `ppc64le` GenericCloud image and uploads it into PowerVC/OpenStack.

```bash
make build-uploadcentos

./UploadCentos/UploadCentos \
  --cloud mycloud \
  --centos CentOS9 \
  --project-upload myproject \
  --svc-host powervc.example.com \
  --template <uuid>
```

| Flag / Env var | Description |
|---|---|
| `--cloud` / `CLOUD` | OpenStack cloud name (required) |
| `--centos` / `CENTOS_VERSION` | `CentOS9` or `CentOS10` (required) |
| `--project-upload` / `PROJECT_UPLOAD` | PowerVC project for the uploaded image (required) |
| `--svc-host` / `SVC_HOST` | PowerVC service host used by `pvcctl` (required) |
| `--template` / `TEMPLATE` | PowerVC template UUID for image import (required) |
| `--project` / `PROJECT` | Optional prefix prepended to image filenames |
| `--date <YYYYMMDD>` | Pin to a specific image build date |
| `--dry-run` | Print commands without executing |
| `-v, --verbose` | Enable debug output |
| `-q, --quiet` | Suppress non-error output |

### UploadRhcos

Downloads RHCOS `ppc64le` images from the OpenShift installer repository and uploads them into PowerVC/OpenStack. Supports multiple release branches and RHEL 9/10 image variants.

```bash
make build-uploadrhcos

./UploadRhcos/UploadRhcos \
  --cloud mycloud \
  --release release-4.21 \
  --rhel rhel9 \
  --project-upload myproject \
  --svc-host powervc.example.com \
  --template <uuid>
```

| Flag / Env var | Description |
|---|---|
| `--cloud` / `CLOUD` | OpenStack cloud name (required) |
| `--project-upload` / `PROJECT_UPLOAD` | PowerVC project for the uploaded image (required) |
| `--release <version>` | Release branch (e.g. `release-4.21`); may be repeated (default: `release-4.21`) |
| `--rhel` / `RHEL_VERSION` | RHEL version for CoreOS image: `rhel9` or `rhel10` (optional) |
| `--svc-host` / `SVC_HOST` | PowerVC service host used by `pvcctl` (required) |
| `--template` / `TEMPLATE` | PowerVC template UUID for image import (required) |
| `--project` / `PROJECT` | Optional prefix prepended to image filenames |
| `--dry-run` | Print commands without executing |
| `-v, --verbose` | Enable debug output |
| `--quiet` | Suppress non-error output |

> **Note:** `UploadCentos` and `UploadRhcos` connect to OpenStack using the CA certificate from the `cacert` field in `clouds.yaml`, so authentication works against PowerVC servers that use a self-signed or privately signed certificate.

---

## CI and Release

GitHub Actions workflows in [`.github/workflows/`](.github/workflows/):

| Workflow | Trigger | Purpose |
|---|---|---|
| `go.yml` | Push or pull request to `main` | Run `make build` and `make test` |
| `release.yml` | GitHub release created | Run `make release` and `make checksums`, then attach the binaries for darwin, linux/amd64, linux/arm64 and linux/ppc64le, the release tarball, and their checksums to the release |

`make release` prepares the same artifacts locally in `dist/`: it runs the tests, builds all platforms, and copies the release scripts (with checksums).

---

## Documentation

| Document | Description |
|---|---|
| [docs/README.md](docs/README.md) | Documentation index and installation prerequisites |
| [docs/easy-installation.md](docs/easy-installation.md) | Quick-start installation guide |
| [docs/complex-installation.md](docs/complex-installation.md) | Advanced installation scenarios |
| [docs/environment-variables.md](docs/environment-variables.md) | Environment variable reference |
| [docs/controller.md](docs/controller.md) | Controller service setup and operation |
| [docs/configure-openstack.md](docs/configure-openstack.md) | OpenStack/PowerVC configuration |
| [docs/IPI-installer.md](docs/IPI-installer.md) | IPI installer integration and RHCOS images |
| [docs/debugging.md](docs/debugging.md) | Troubleshooting guide |
| [docs/tools.md](docs/tools.md) | Required external tools |
| [docs/powervc-ci-lifecycle.md](docs/powervc-ci-lifecycle.md) | PowerVC CI lifecycle flowchart (also [`powervc-ci-lifecycle.png`](powervc-ci-lifecycle.png)) |
| [docs/create-cluster-flowchart.mmd](docs/create-cluster-flowchart.mmd) | Mermaid flowchart of the cluster creation flow |
| [docs/IBMCloud-documentation.md](docs/IBMCloud-documentation.md) | `IBMCloud.go` IBM Cloud SDK wrappers with retry logic |
| [docs/IBM-DNS-documentation.md](docs/IBM-DNS-documentation.md) | `IBM-DNS.go` DNS record management |
| [docs/Utils-documentation.md](docs/Utils-documentation.md) | `Utils.go` utility functions |
| [docs/bugs-and-risks-CmdWatchInstallation.md](docs/bugs-and-risks-CmdWatchInstallation.md) | Known bugs and risks in `CmdWatchInstallation.go` |
| [docs/testing-ssh-permission-denied-errors.md](docs/testing-ssh-permission-denied-errors.md) | How the SSH permission-denied error in `CmdCreateBastion.go` is tested |

Dated code-review and improvement notes for each command are kept in [`improvements/`](improvements/). The [`ansible/`](ansible/) directory contains `install-cluster-master.yml`, a playbook that sets up a host: it installs packages, configures `dhcpd`, checks out and installs this tool, installs Go and `oc`, and writes `clouds.yaml`. It requires the variables `username`, `password`, `ip`, `project_id` and `project_name`.

---

## License

Apache License 2.0 — see [LICENSE](LICENSE).
