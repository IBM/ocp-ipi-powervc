# Configuring OpenStack

## 1. Install the CLI

We use the OpenStack CLI to perform administration tasks.  The tools are Python based and can be installed, for example, like this:

```
$ sudo dnf config-manager --set-enabled crb
$ sudo dnf install -y centos-release-openstack-dalmatian.noarch
$ sudo dnf install -y python3-openstackclient
```

On the latest version of CentOS (version 10), I was not able to find this package.  In this case, you can use CentOS9 

## 2. Optionally configure the PowerVC Root CA

If your IBM PowerVC uses a custom Root CA, you must register it so the OpenStack client can communicate securely.

Download the PEM file for your PowerVC instance to the anchors folder:

```
# echo "" | openssl s_client -showcerts -prexit -connect my-powervc.ibm.net:443 2> /dev/null | sed -n -e '/BEGIN CERTIFICATE/,/END CERTIFICATE/ p' > /etc/pki/ca-trust/source/anchors/my-powervc-443.crt
```

Check to see that it is a PEM file:

```
# file /etc/pki/ca-trust/source/anchors/my-powervc-443.crt
/etc/pki/ca-trust/source/anchors/my-powervc-443.crt: PEM certificate
```

Update the System Trust Store:

```
# update-ca-trust
```

You can verify (no -k used):

```
# curl https://mypowervc.ibm.net:8443 -o/dev/null
  % Total    % Received % Xferd  Average Speed   Time    Time     Time  Current
                                 Dload  Upload   Total   Spent    Left  Speed
100 78321  100 78321    0     0  3186k      0 --:--:-- --:--:-- --:--:-- 3186k
```

Copy the powervc cert into the right location:

```
# mkdir -p ~/.config/openstack/
# cp /etc/pki/ca-trust/source/anchors/my-powervc-443.crt ~/.config/openstack/
```

## 3. Add an entry to clouds.yaml

Make sure that the file `~/.config/openstack/clouds.yaml` exists and has the example contents:

```
clouds:
  powervc:
    auth:
      auth_url: https://mypowervc.ibm.net:5000
      username: "your_username"
      password: "your_password"
      project_id: your_project_id
      project_name: "your_project_name"
      user_domain_name: "Default"
    cacert: /home/directory-name/.config/openstack/powervc-ca.pem
    region_name: "RegionOne"
    interface: "public"
    identity_api_version: 3
```

## 4. Verify the configuration works

Now verify that the environment has been configured correctly:

```
openstack --os-cloud=${CLOUD} server list
```
