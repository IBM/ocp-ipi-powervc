# UploadRhcos

Downloads Red Hat CoreOS (RHCOS) images from the OpenShift installer repository
and uploads them into a PowerVC/OpenStack environment, replacing the
`scripts/upload-rhcos.sh` shell script with a self-contained Go binary.

## Overview

For each requested release the program:

1. Fetches CoreOS JSON metadata from the `openshift/installer` GitHub repository.
2. Extracts the `ppc64le` OpenStack `qcow2.gz` image URL, filename, and SHA-256.
3. Checks whether the image already exists in OpenStack via the `openstack` CLI.
4. If the image is absent, optionally converts it to OVA format with `pvsadm`,
   then imports it into PowerVC with `pvcctl` or `powervc-image`.

## Dependencies

| Tool | Required | Purpose |
|------|----------|---------|
| `openstack` | **Yes** | Image existence check and connectivity verification |
| `pvcctl` | One of these two | PowerVC image import (preferred) |
| `powervc-image` | One of these two | PowerVC image import (fallback) |
| `pvsadm` | No | qcow2 → OVA conversion before `powervc-image` import |

> **Note:** `pvsadm` is optional. When absent, `pvcctl` imports directly from
> the remote URL. When `powervc-image` is used (no `pvcctl`), `pvsadm` **must**
> be present — `powervc-image` only imports local OVA files.

## Building

```bash
# From the repo root
make build-uploadrhcos

# Or directly inside this directory
go build -o UploadRhcos .
```

To (re-)initialise the Go module from scratch:

```bash
make init-uploadrhcos
```

To install the binary to `$GOPATH/bin`:

```bash
make install-uploadrhcos
```

## Usage

```
UploadRhcos [OPTIONS]
```

### Flags

| Flag | Env var | Default | Description |
|------|---------|---------|-------------|
| `--cloud <name>` | `CLOUD` | — | OpenStack cloud name from `clouds.yaml` |
| `--project <name>` | `PROJECT` | — | Optional prefix prepended to image filenames (trailing `-` stripped) |
| `--project-upload <name>` | `PROJECT_UPLOAD` | — | PowerVC project for image upload |
| `--release <version>` | — | `release-4.21` | Release branch to process; may be repeated |
| `--rhel <rhel9\|rhel10>` | `RHEL_VERSION` | — | RHEL version preference for CoreOS JSON selection |
| `--svc-host <host>` | `SVC_HOST` | — | PowerVC service host |
| `--template <uuid>` | `TEMPLATE` | — | PowerVC template UUID |
| `-v`, `--verbose` | — | false | Enable `[DEBUG]` output |
| `--dry-run` | — | false | Print commands without executing them |
| `--quiet` | — | false | Suppress all non-error output |
| `-h`, `--help` | — | — | Show usage and exit |

All flags can also be supplied via their corresponding environment variable.
If a required value is missing and stdin is a terminal the program prompts
interactively.

### Examples

```bash
# All options on the command line
UploadRhcos \
  --cloud mycloud \
  --project-upload myproject \
  --release release-4.21 \
  --rhel rhel9 \
  --svc-host powervc.example.com \
  --template xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx

# Multiple releases
UploadRhcos --release release-4.21 --release release-4.22 --rhel rhel9

# From environment variables (non-interactive)
export CLOUD=mycloud
export PROJECT_UPLOAD=myproject
export RHEL_VERSION=rhel9
export SVC_HOST=powervc.example.com
export TEMPLATE=xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx
UploadRhcos --release release-4.21

# Dry run — prints every command without executing it
UploadRhcos --release release-4.21 --dry-run

# Verbose debug output
UploadRhcos --release release-4.21 --verbose
```

## Workflow

```
parse flags
    ↓
check required programs  (openstack; pvcctl or powervc-image; optionally pvsadm)
    ↓
collect missing config   (env vars → interactive prompts)
    ↓
validate config
    ↓
verify OpenStack connectivity
    ↓
for each --release:
    download coreos JSON from GitHub   (rhel version preference applied)
        ↓
    extract ppc64le qcow2.gz URL, filename, SHA-256
        ↓
    image already in OpenStack? ──yes──→ skip
        ↓ no
    pvsadm available?  ──yes──→ convert qcow2.gz → OVA
        ↓
    pvcctl available?  ──yes──→ pvcctl image import-linux  (remote URL)
                       ──no───→ powervc-image import       (local OVA)
```

## CoreOS JSON URL selection

The program tries up to three candidate URLs per release, preferring the one
that matches `--rhel`:

| `--rhel` | Priority order |
|----------|---------------|
| `rhel9` | `coreos-rhel-9.json` → `rhcos.json` → `coreos-rhel-10.json` |
| `rhel10` | `coreos-rhel-10.json` → `rhcos.json` → `coreos-rhel-9.json` |
| *(unset)* | `rhcos.json` → `coreos-rhel-9.json` → `coreos-rhel-10.json` |

A non-200 HTTP response skips to the next URL immediately. Network errors are
retried up to 3 times with a 2-second delay.

## Exit codes

| Code | Meaning |
|------|---------|
| `0` | All releases processed successfully |
| `1` | One or more releases failed, or a fatal configuration error occurred |
| `2` | Invalid command-line flag (handled by the Go `flag` package) |
