WARNING: **staging** is meant for testing with Let's Encrypt and will provide certificates signed by testing CA making the certs untrusted.
```bash
    oc create -fdeploy/{clusterrole,serviceaccount,imagestream,deployment}.yaml
    oc adm policy add-cluster-role-to-user openshift-acme -z openshift-acme
```
