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
