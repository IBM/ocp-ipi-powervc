# PowerVC

A PowerVC cluster is currently beta level supported in the IPI (Installer Provisioned Infrastructure) installer.o

The OpenShift documentation is located [here](https://docs.redhat.com/en/documentation/openshift_container_platform/4.21/html-single/installing_on_ibm_powervc/index).  It uses the IPI OpenStack installer for the majority of the work.  Documentation for the OpenStack installer is located [here](https://docs.redhat.com/en/documentation/openshift_container_platform/4.21/html-single/installing_on_openstack/index).

To install a PowerVC cluster, you need to have many things configured:

1) A PowerVC server
2) A valid OpenStack environment for CLI administration (configured [here](https://github.com/IBM/ocp-ipi-powervc/blob/main/docs/configure-openstack.md))
3) A controller VM running code from this repo (configured [here](https://github.com/IBM/ocp-ipi-powervc/blob/main/docs/controller.md))
4) Environment variables defined (explained [here](https://github.com/IBM/ocp-ipi-powervc/blob/main/docs/environment-variables.md))
5) The OpenShift IPI installer and RHCOS image (explained [here](https://github.com/IBM/ocp-ipi-powervc/blob/main/docs/IPI-installer.md))

There is an easy installation script you can use described [here](https://github.com/IBM/ocp-ipi-powervc/blob/main/docs/easy-installation.md).

Or, there is a set of rather complex installation steps you can follow [here](https://github.com/IBM/ocp-ipi-powervc/blob/main/docs/complex-installation.md).

If you have problems, then follow the debugging steps [here](https://github.com/IBM/ocp-ipi-powervc/blob/main/docs/debugging.md).
