# Download the OpenShift installer for a release

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
```

Note that if you have access to [authenticating to the app.ci cluster.](https://docs.ci.openshift.org/docs/how-tos/use-registries-in-build-farm/), then you can install release images from [ppc64le release images](https://ppc64le.ocp.releases.ci.openshift.org/).
