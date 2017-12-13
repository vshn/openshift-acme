```bash
ssh -N -R 'localhost:45000:localhost:80' your.public.server.io
```

```bash
oc create -f{service,deployment}.yaml
```

```bash
ssh -4 -o UserKnownHostsFile=/dev/null -o StrictHostKeyChecking=no root@`oc get svc acme-controller -n acme -o template --template='{{.spec.clusterIP}}'` -p 2222 -N -R '0.0.0.0:6000:localhost:5000'
```

```bash
go install -v && go run main.go --kubeconfig=$KUBECONFIG --exposer-ip=`oc get -n acme ep/acme-controller -o template --template '{{ (index (index .subsets 0).addresses 0).ip }}'` --loglevel=5
```

```bash
oc adm policy add-cluster-role-to-user cluster-admin system:serviceaccount:acme:default
```
