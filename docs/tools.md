# Tools

# Install jq

jq is part of CentOS.  It parses and operates on the JSON file format.

```
$ sudo dnf install -y jq
```

# Install yq

yq exists on GitHub.  It parses and operates on the YAML file format.

```
$ (cd ~/bin/; curl --silent --output yq --location https://github.com/mikefarah/yq/releases/download/v4.48.1/yq_linux_ppc64le && chmod u+x yq)
```

# Install pvsadm

pvsadm converts QCOW image format to OVA format.  It is found [here](https://github.com/ppc64le-cloud/pvsadm).  Download the latest release binary.

```
$ (cd ~/bin/; curl --silent --output pvsadm --location https://github.com/ppc64le-cloud/pvsadm/releases/download/v0.1.24/pvsadm-linux-amd64; chmod u+x pvsadm)
```

# IBM Cloud CLI

This is required for managing DNS records via IBM Cloud Internet Services (CIS).

You can either register the records in your system locally with CLI commands or let the tool's [controller](https://github.com/IBM/ocp-ipi-powervc/blob/main/docs/controller.md) handle that for you.

```
$ curl -fsSL https://clis.cloud.ibm.com/install/linux | sh
$ for PLUGIN in dns cis; do ibmcloud plugin install ${PLUGIN}; done
```
