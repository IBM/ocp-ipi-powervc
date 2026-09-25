# Snippet 6: OpenShift PersistentVolumeClaim (PVC) Management & Troubleshooting

This program (`snippet6.go`) demonstrates creating and managing OpenShift `PersistentVolumeClaim` (PVC) resources natively using the Kubernetes Go client (`k8s.io/client-go`).

---

## 1. Why the PVC was initially in `Pending` Status

When creating a PVC with the default StorageClass (`standard-csi`), the claim initially remains in the `Pending` state:

```text
NAMESPACE   NAME        STATUS    VOLUME   CAPACITY   ACCESS MODES   STORAGECLASS   AGE
default     hamzy-pvc   Pending                                      standard-csi   1m
```

### Explanation
Inspecting the StorageClass:
```yaml
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: standard-csi
provisioner: cinder.csi.openstack.org
volumeBindingMode: WaitForFirstConsumer
```

With `volumeBindingMode: WaitForFirstConsumer`, OpenShift/Kubernetes intentionally delays volume creation and binding until a Pod referencing the PVC is scheduled. This ensures the volume is provisioned in the same availability zone and failure domain as the scheduled worker node.

---

## 2. Why `pod/test-consumer-pod` Stuck in `ContainerCreating`

When creating a consumer pod to bind and mount the PVC:

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: test-consumer-pod
  namespace: default
spec:
  containers:
  - name: test-container
    image: registry.redhat.io/ubi9/ubi-minimal:latest
    command: ["sleep", "3600"]
    volumeMounts:
    - mountPath: /mnt/storage
      name: test-vol
  volumes:
  - name: test-vol
    persistentVolumeClaim:
      claimName: hamzy-pvc
```

The pod was scheduled to worker node `rdr-ci-openstack-xkqb9-worker-0-rg4bw`, and volume attachment succeeded:
```text
Normal  SuccessfulAttachVolume  AttachVolume.Attach succeeded for volume "pvc-6d599847-bed9-4ef0-a84f-dd43de50810f"
```

However, the pod remained stuck in `ContainerCreating`.

### Root Cause Analysis

Inspecting the CSI node driver pod logs on the worker node (`openstack-cinder-csi-driver-node-mmj69` in namespace `openshift-cluster-csi-drivers`):

```text
W0924 04:35:00.510709 1 nodeserver.go:416] Couldn't get device path from mount: failed to find device for the volumeID: "70602b5c-bef7-48ec-9ade-d4ca620d9279" within the alloted time
I0924 04:35:00.510755 1 nodeserver.go:421] Trying to get device path from metadata service
E0924 04:35:30.511334 1 metadata.go:229] Could not retrieve instance metadata: error fetching http://169.254.169.254/openstack/latest/meta_data.json: Get "http://169.254.169.254/openstack/latest/meta_data.json": dial tcp 169.254.169.254:80: i/o timeout
E0924 04:35:30.511392 1 nodeserver.go:424] Couldn't get device path from metadata service: could not retrieve instance metadata: error fetching http://169.254.169.254/openstack/latest/meta_data.json: Get "http://169.254.169.254/openstack/latest/meta_data.json": dial tcp 169.254.169.254:80: i/o timeout
E0924 04:35:30.511422 1 utils.go:99] [ID:967] GRPC error: rpc error: code = Internal desc = Unable to find Device path for volume: couldn't get device path from metadata service: could not retrieve instance metadata: error fetching http://169.254.169.254/openstack/latest/meta_data.json: Get "http://169.254.169.254/openstack/latest/meta_data.json": dial tcp 169.254.169.254:80: i/o timeout
```

### Underlying Reasons in PowerVC / OpenStack

1. **Local Device Path Resolution Failure**:
   - The Cinder CSI node driver monitors `/dev/disk/by-id/` for the newly attached volume serial / WWN.
   - On PowerVM (vSCSI / NPIV adapters in PowerVC), new SCSI disk devices may not immediately show up or may expose SCSI inquiry strings / WWNs differently than standard KVM virtio-blk devices.

2. **Metadata Service Fallback Failure (`169.254.169.254`)**:
   - When local device lookup times out, the Cinder CSI node driver falls back to querying OpenStack's link-local metadata service at `http://169.254.169.254/openstack/latest/meta_data.json` to get volume-to-device mapping.
   - In this test cluster environment, the metadata service route / proxy is either not routed on the worker network or blocked, resulting in `dial tcp 169.254.169.254:80: i/o timeout`.
   - As a result, the node driver cannot determine the `/dev/` device node to format/mount, causing the container mount step to block in `ContainerCreating`.

---

## 3. How to Identify the CSI Driver Pod for a Worker Node

1. **Find the node running the pod**:
   ```bash
   oc get pod test-consumer-pod -n default -o wide
   ```
   *Example output: `NODE: rdr-ci-openstack-xkqb9-worker-0-rg4bw`*

2. **Find the CSI daemonset pod running on that specific node**:
   ```bash
   oc get pod -n openshift-cluster-csi-drivers -o wide --field-selector spec.nodeName=rdr-ci-openstack-xkqb9-worker-0-rg4bw
   ```
   *Example output: `openstack-cinder-csi-driver-node-mmj69`*

3. **Check the CSI node driver logs**:
   ```bash
   oc logs openstack-cinder-csi-driver-node-mmj69 -n openshift-cluster-csi-drivers -c csi-driver --tail=30
   ```

---

## 4. In-Node Debugging Guide (`oc debug node/<node-name>`)

When accessing the worker node shell (`oc debug node/<node>` followed by `chroot /host`), run the following commands to diagnose device discovery and metadata connectivity:

### Step 1: Check Attached Block Devices & Symlinks
The Cinder CSI driver looks for symlinks matching the Cinder volume UUID in `/dev/disk/by-id/`.

```bash
# List all device symlinks by ID
ls -la /dev/disk/by-id/

# List all device symlinks by path
ls -la /dev/disk/by-path/

# Inspect all block devices, SCSI model, serial numbers, and WWNs
lsblk -o NAME,SIZE,TYPE,FSTYPE,MOUNTPOINT,MODEL,SERIAL,WWN
```

### Step 2: Check SCSI Events & Trigger Bus Rescan
Check if the Linux kernel received the attachment notification, and force a SCSI bus rescan if needed:

```bash
# Check kernel ring buffer for SCSI device attachments
dmesg -T | grep -iE 'scsi|sd[a-z]|attachment|multipath' | tail -n 50

# Rescan all SCSI hosts on the worker node
for host in /sys/class/scsi_host/host*/scan; do
    echo "- - -" > "$host"
done

# Check if new block devices appeared after rescan
lsblk
```

### Step 3: Test OpenStack Metadata Service Connectivity
The CSI driver attempts to reach the link-local metadata address `169.254.169.254:80` when local `/dev/disk/by-id` matching fails.

```bash
# Test metadata HTTP endpoint
curl -v --connect-timeout 5 http://169.254.169.254/openstack/latest/meta_data.json

# Check routing table for link-local or default route
ip route get 169.254.169.254
ip route show

# Check firewall / packet filtering rules
iptables -L -n -v | grep 169.254
```

### Step 4: Check Multipath Configuration (SAN / FibreChannel)
If using PowerVC FibreChannel/NPIV storage:

```bash
# Check multipath topology
multipath -ll

# Check multipath daemon status
systemctl status multipathd
```
