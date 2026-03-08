# controller VM

There is expected to be a VM which contains the ocp-ipi-powervc helper utility.  This program is responsible for a number of tasks:

1) Update dhcpd configuration
2) Update DNS configuration
3) Update bastion configurations

Create a CentOS 9 VM.  Then run this ansible script: [ansible/install-cluster-master.yml](https://github.com/IBM/ocp-ipi-powervc/blob/main/ansible/install-cluster-master.yml).

```
$ cd ocp-ipi-powervc/ansible
$ ansible-playbook -i inventory install-cluster-master.yml --extra-vars "username=${USERNAME} password=${PASSWORD} project_id=${PROJECT_ID}"
```

It is recommended to use `tmux` to keep the session alive.

In one window, run the following command:

```
$ export IBMCLOUD_API_KEY="your-key"
$ ocp-ipi-powervc-$(uname -m) \
	watch-installation \
	--cloud "${CLOUD}" \
	--domainName "${BASEDOMAIN}" \
	--bastionMetadata /home/cloud-user \
	--bastionUsername cloud-user \
	--bastionRsa /home/cloud-user/.ssh/id_powervc \
	--enableDhcpd true \
	--dhcpInterface eth0 \
	--dhcpSubnet ${DHCP_SUBNET} \
	--dhcpNetmask ${DHCP_NETMASK} \
	--dhcpRouter ${DHCP_ROUTER} \
	--dhcpDnsServers "${DHCP_SERVERS}" \
	--dhcpServerId ${DHCP_SERVER_ID} \
	--shouldDebug true
```

The `IBMCLOUD_API_KEY` environment variable is optional.  If set, then the program will update DNS entries based on the bastion metadata that it receives.

`--enableDhcpd` is optional.  The other `--dhcpXXX` arguments are required if DHCP is enabled.
