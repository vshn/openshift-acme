WARNING: **staging** is meant for testing with Let's Encrypt and will provide certificates signed by testing CA making the certs untrusted.
```bash
    oc create -fdeploy/{role,serviceaccount,imagestream,deployment}.yaml
    oc policy add-role-to-user openshift-acme --role-namespace="$(oc project --short)" -z openshift-acme
```
