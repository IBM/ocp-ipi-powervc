# UploadRhcosAPI

Downloads Red Hat CoreOS (RHCOS) images from the OpenShift installer repository
and uploads them into a PowerVC/OpenStack environment using direct API calls.

## Overview

For each requested release the program:

1. Fetches CoreOS JSON metadata from the `openshift/installer` GitHub repository.
2. Extracts the `ppc64le` OpenStack `qcow2.gz` image URL, filename, and SHA-256.
3. Checks whether the image already exists in OpenStack via the gophercloud Glance API.
4. If the image is absent, POSTs an import request to the PowerVC image import service.
5. Polls the job status endpoint until the import reaches a terminal state (`done` / `failed`).

## Building

```bash
# From the repo root
make build-uploadrhcosapi

# Or directly inside this directory
go build -o UploadRhcosAPI .
```

To (re-)initialise the Go module from scratch:

```bash
make init-uploadrhcosapi
```

To install the binary to `$GOPATH/bin`:

```bash
make install-uploadrhcosapi
```

## Usage

```
UploadRhcosAPI [OPTIONS]
```

### Flags

Only the following flags are accepted on the command line. All other parameters
must be supplied via environment variables (see below).

| Flag | Env var | Default | Description |
|------|---------|---------|-------------|
| `--ca-cert <path>` | `CACERT` | — | Path to PEM CA certificate for import service TLS; auto-detected from `clouds.yaml` when `--cloud` is set |
| `--cloud <name>` | `CLOUD` | — | OpenStack cloud name from `clouds.yaml` |
| `--connect-timeout <d>` | `CONNECT_TIMEOUT` | `2m` | Timeout for the OpenStack connectivity check at startup (Go duration, e.g. `30s`, `2m`) |
| `--release <version>` | — | `release-4.21` | Release branch to process; may be repeated |
| `--rhel <rhel9\|rhel10>` | `RHEL_VERSION` | — | RHEL version preference for CoreOS JSON selection |
| `--insecure` | — | `false` | Skip TLS certificate verification for the import service |
| `-v`, `--verbose` | — | `false` | Enable `[DEBUG]` output |
| `--dry-run` | — | `false` | Simulate operations without executing them |
| `--log-payload` | — | `false` | Print the outgoing import JSON payload (credentials redacted) |
| `--quiet` | — | `false` | Suppress all non-error output |
| `-h`, `--help` | — | — | Show usage and exit |

### Environment variables

All sensitive and site-specific parameters are supplied via environment
variables. If a required variable is missing and stdin is a terminal the
program prompts interactively; otherwise it exits with an error.

**Required:**

| Variable | Description |
|----------|-------------|
| `PROJECT_UPLOAD` | PowerVC project/tenant name for image upload |
| `RHEL_VERSION` | RHEL version preference (`rhel9` or `rhel10`) |
| `SVC_HOST` | PowerVC service host |
| `SVC_USER` | PowerVC service username |
| `SVC_PASSWORD` | PowerVC service password |
| `POWERVC_URL` | PowerVC Keystone URL (e.g. `https://<host>:5000/v3`) |
| `POWERVC_USER` | PowerVC username |
| `POWERVC_PASSWORD` | PowerVC password |
| `TEMPLATE` | PowerVC template UUID |

**Optional:**

| Variable | Default | Description |
|----------|---------|-------------|
| `CACERT` | — | Path to PEM CA certificate for import service TLS |
| `CLOUD` | — | OpenStack cloud name from `clouds.yaml` |
| `CONNECT_TIMEOUT` | `2m` | Timeout for the OpenStack connectivity check (Go duration, e.g. `30s`, `2m`) |
| `IMPORT_SERVICE_URL` | `https://<powervc-host>:8181/images/import` | Image import service URL |
| `OS_TYPE` | `coreos` | Operating system type |
| `POWERVC_TENANT` | same as `PROJECT_UPLOAD` | PowerVC tenant/project name |
| `PROJECT` | — | Optional prefix prepended to image filenames (trailing `-` stripped) |
| `VOLUME_SIZE` | `120` | Volume size in GB |

### Examples

```bash
# Typical invocation — sensitive values via environment variables
export PROJECT_UPLOAD=ocp-ci
export RHEL_VERSION=rhel9
export SVC_HOST=powervc.example.com
export SVC_USER=someuser
export SVC_PASSWORD=somepassword
export POWERVC_URL=https://powervc.example.com:5000/v3
export POWERVC_USER=someuser
export POWERVC_PASSWORD=somepassword
export TEMPLATE=xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx
UploadRhcosAPI --cloud mycloud --release release-4.21 --rhel rhel9

# Multiple releases
UploadRhcosAPI --release release-4.21 --release release-4.22

# Dry run — simulates operations without executing them
UploadRhcosAPI --release release-4.21 --dry-run

# Verbose debug output
UploadRhcosAPI --release release-4.21 --verbose

# Print the outgoing import JSON payload (useful for debugging auth issues)
UploadRhcosAPI --release release-4.21 --log-payload

# Shorter startup timeout (useful on slow networks)
UploadRhcosAPI --release release-4.21 --connect-timeout 30s
```

## Job polling

After a successful POST the import service returns a job ID and a `poll_at` URL.
The program polls that URL every 15 seconds (up to 1 hour) and logs the job
progress until a terminal state is reached:

| Status | Meaning |
|--------|---------|
| `queued`, `pending`, `running`, `acquiring`, `extracting`, `importing` | In progress |
| `done`, `completed` | Success |
| `failed`, `error` | Failure — error message logged and exit code 1 |

## Exit codes

| Code | Meaning |
|------|---------|
| `0` | All releases processed successfully |
| `1` | One or more releases failed, or a fatal configuration error occurred |
| `2` | Invalid command-line flag (handled by the Go `flag` package) |
