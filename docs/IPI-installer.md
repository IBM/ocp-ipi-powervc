# Download the OpenShift installer for a release

## IPI installer

You can download the OpenShift IPI installer from the RedHat mirror site.  Find the latest version, an example is [4.21.5](https://mirror.openshift.com/pub/openshift-v4/clients/ocp/4.21.5/).  Then download and expand the tarball matching the following pattern:

```
openshift-install-${OS}-${ARCH}.tar.gz
```

An example would be:

```
$ tar xvzf openshift-install-linux-arm64.tar.gz
```

The other method is to use the `oc adm` command if you have the `oc` tool already installed.  NOTE: The `oc` tool is also in this directory in the `openshift-client-${OS}-${ARC}.tar.gz`.  For example:

```
$ (mkdir 4.22.0-ec.2; cd 4.22.0-ec.2/; oc adm -a ${HOME}/.pullSecretCompact release extract --tools quay.io/openshift-release-dev/ocp-release:4.22.0-ec.2-ppc64le; tar xvzf openshift-install-linux-amd64-4.22.0-ec.2.tar.gz)

or

$ (mkdir 4.22.0-ec.2; cd 4.22.0-ec.2/; oc adm -a ${HOME}/.pullSecretCompact release extract --command openshift-install quay.io/openshift-release-dev/ocp-release:4.22.0-ec.2-ppc64le; oc adm -a ${HOME}/.pullSecretCompact release extract --command oc quay.io/openshift-release-dev/ocp-release:4.22.0-ec.2-ppc64le)
```

Note that if you have access to [authenticating to the app.ci cluster.](https://docs.ci.openshift.org/docs/how-tos/use-registries-in-build-farm/), then you can install release images from [ppc64le release images](https://ppc64le.ocp.releases.ci.openshift.org/).

## RHCOS image

To find the correct RHCOS image to use, you need the IPI installer.  Ensure the RHCOS image is uploaded to PowerVC. Extract the URL from the installer and check for it:

```
$ URL=$(openshift-install coreos print-stream-json | jq -r '.architectures.ppc64le.artifacts.openstack' | jq -r '.formats."qcow2.gz".disk.location')
$ echo ${URL} 
https://rhcos.mirror.openshift.com/art/storage/prod/streams/rhel-9.6/builds/9.6.20251023-0/ppc64le/rhcos-9.6.20251023-0-openstack.ppc64le.qcow2.gz
$ FILENAME="${URL##*/}"
$ FILENAME=${FILENAME//.qcow2.gz/}
$ echo ${FILENAME}
rhcos-9.6.20251023-0-openstack.ppc64le
$ openstack --os-cloud=powervc image list --format=csv | grep rhcos-9.6.20251023-0-openstack.ppc64le
"406e2dcf-7391-4cf8-90a9-e412668f5242","rhcos-9.6.20251023-0-openstack.ppc64le","active"
```

If it doesn't exist, then convert it and upload it:

```
$ pvsadm image qcow2ova --image-dist coreos --image-name rhcos-9.6.20251023-0-openstack.ppc64le --image-url https://rhcos.mirror.openshift.com/art/storage/prod/streams/rhel-9.6/builds/9.6.20251023-0/ppc64le/rhcos-9.6.20251023-0-openstack.ppc64le.qcow2.gz --image-size 16
$ powervc-image --project ocp-ipi import -n rhcos-9.6.20251023-0-openstack.ppc64le -p rhcos-9.6.20251023-0-openstack.ppc64le.ova.gz -t ... -m os-type=coreos architecture=ppc64le
$ openstack --os-cloud=powervc image list --format=csv | grep rhcos-9.6.20251023-0-openstack.ppc64le
"406e2dcf-7391-4cf8-90a9-e412668f5242","rhcos-9.6.20251023-0-openstack.ppc64le","active"
```
