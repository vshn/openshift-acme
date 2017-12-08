```bash
    oc create -fdeploy/{role,serviceaccount,imagestream,deployment}.yaml
    oc policy add-role-to-user openshift-acme --role-namespace="$(oc project --short)" -z openshift-acme
```
