```bash
    oc create -fdeploy/{clusterrole,serviceaccount,imagestream,deployment}.yaml
    oc adm policy add-cluster-role-to-user openshift-acme -z openshift-acme
```
