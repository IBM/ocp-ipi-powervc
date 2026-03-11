# Easy installation method

Before you start the easy installation method, make sure you satisfy all of the requirements of the root document README [here](https://github.com/IBM/ocp-ipi-powervc/blob/main/docs/README.md).

# Create an OpenShift cluster

With everything configured, you can now run `scripts/create-cluster.sh`.  For example:

```
$ (export PATH=${PATH}:${HOME}/ocp-ipi-powervc/:${HOME}/4.22.0-ec.2/; source environment; [ -d "${CLUSTER_DIR}" ] && rm -rf "${CLUSTER_DIR}/"; ${HOME}/ocp-ipi-powervc/scripts/create-cluster.sh)
```

# Delete an OpenShift cluster

To cleanup, run `scripts/delete-cluster.sh`.  For example:

```
$ (export PATH=${PATH}:${HOME}/ocp-ipi-powervc/:${HOME}/4.22.0-ec.2/; source environment; [ ! -d "${CLUSTER_DIR}" ] && exit 1; ${HOME}/ocp-ipi-powervc/scripts/delete-cluster.sh)
```
