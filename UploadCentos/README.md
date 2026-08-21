# UploadCentos

Downloads CentOS Stream GenericCloud images from the official CentOS cloud
mirror and uploads them into a PowerVC/OpenStack environment, replacing the
`scripts/upload-centos.sh` shell script with a self-contained Go binary.

## Overview

The program:

1. Fetches the HTML directory listing from `cloud.centos.org` to find the
   latest (or pinned) dated CentOS Stream GenericCloud `qcow2` image for `ppc64le`.
2. Derives the bare image name from the URL.
3. Checks whether the image already exists in OpenStack via the `openstack` CLI.
4. If the image is absent, optionally converts it to OVA format with `pvsadm`,
   then imports it into PowerVC with `pvcctl` or `powervc-image`.

## Dependencies

| Tool | Required | Purpose |
|------|----------|---------|
| `openstack` | **Yes** | Connectivity verification and image existence checks |
| `pvcctl` | One of these two | PowerVC image import (preferred) |
| `powervc-image` | One of these two | PowerVC image import (fallback) |
| `pvsadm` | No | qcow2 → OVA conversion before import |

> **Note:** `pvsadm` is optional. When absent, `pvcctl` imports directly from
> the remote URL. When `powervc-image` is used (no `pvcctl`), `pvsadm` **must**
> be present — `powervc-image` only imports local OVA files.

## Building

```bash
# From the repo root
make build-uploadcentos

# Or directly inside this directory
go build -o UploadCentos .
```

To (re-)initialise the Go module from scratch:

```bash
make init-uploadcentos
```

To install the binary to `$GOPATH/bin`:

```bash
make install-uploadcentos
```

## Usage

```
UploadCentos [OPTIONS]
```

### Flags

| Flag | Env var | Default | Description |
|------|---------|---------|-------------|
| `--cloud <name>` | `CLOUD` | — | OpenStack cloud name from `clouds.yaml` |
| `--centos <CentOS9\|CentOS10>` | `CENTOS_VERSION` | — | CentOS Stream version |
| `--date <YYYYMMDD>` | — | latest | Pin to a specific image build date |
| `--project <name>` | `PROJECT` | — | Optional prefix prepended to image filenames (trailing `-` stripped) |
| `--project-upload <name>` | `PROJECT_UPLOAD` | — | PowerVC project for image upload |
| `--svc-host <host>` | `SVC_HOST` | — | PowerVC service host |
| `--template <uuid>` | `TEMPLATE` | — | PowerVC template UUID |
| `-v`, `--verbose` | — | false | Enable `[DEBUG]` output |
| `--dry-run` | — | false | Print commands without executing them |
| `-q`, `--quiet` | — | false | Suppress all non-error output |
| `-h`, `--help` | — | — | Show usage and exit |

All flags can also be supplied via their corresponding environment variable.
If a required value is missing and stdin is a terminal the program prompts
interactively.

### Examples

```bash
# All options on the command line
UploadCentos \
  --cloud mycloud \
  --project-upload myproject \
  --centos CentOS9 \
  --svc-host powervc.example.com \
  --template xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx

# Pin to a specific build date
UploadCentos --centos CentOS9 --date 20260720

# From environment variables (non-interactive)
export CLOUD=mycloud
export PROJECT_UPLOAD=myproject
export CENTOS_VERSION=CentOS9
export SVC_HOST=powervc.example.com
export TEMPLATE=xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx
UploadCentos

# Dry run — prints every command without executing it
UploadCentos --centos CentOS9 --dry-run

# Verbose debug output
UploadCentos --centos CentOS9 --verbose
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
fetch CentOS image listing from cloud.centos.org
    ↓
select latest (or pinned) dated qcow2 filename
apply --project prefix if set
    ↓
image already in OpenStack? ──yes──→ done (exit 0)
    ↓ no
pvsadm available?  ──yes──→ convert qcow2 → OVA
    ↓
pvcctl available?  ──yes──→ pvcctl image import-linux  (local OVA or remote URL)
                   ──no───→ powervc-image import        (local OVA, requires pvsadm)
```

## Exit codes

| Code | Meaning |
|------|---------|
| `0` | Image processed successfully (uploaded or already present) |
| `1` | A step failed, or a fatal configuration error occurred |
| `2` | Invalid command-line flag (handled by the Go `flag` package) |
