#!/bin/bash
set -e

wd=$(pwd)

pushd $(dirname $(realpath -s $0))/..

if [[ "$1" = /* ]]
then
   # Absolute path
   bindir=$1
else
   # Relative path
   bindir=${wd}/${1:-.}
fi
prefix=${bindir}/oc-
binaries=$(echo ${prefix}*)
echo binaries: ${binaries}

[[ ! -z "${binaries}" ]]

make -j64 test-extended GOFLAGS='-i -race' TEST_FLAGS=''

for binary in ${binaries}; do
    version=${binary#$prefix}
    echo version: ${version}
    alias oc=${binary}
    oc cluster up --version=${version}
    oc login -u system:admin

    # Wait for docker-registry
    oc rollout status -n default dc/docker-registry
    # Wait for router
    oc rollout status -n default dc/router

    oc get all -n default

    oc new-project acme-aaa
    oc get sa,secret

    oc create -fdeploy/letsencrypt-staging/cluster-wide/{clusterrole,serviceaccount,imagestream,deployment}.yaml
    oc adm policy add-cluster-role-to-user openshift-acme -z openshift-acme
    oc patch is openshift-acme -p '{"spec":{"tags":[{"name":"latest","importPolicy":{"scheduled": false}}]}}'

    sa_secret_name=$(oc get sa builder --template='{{ (index .imagePullSecrets 0).name }}')
    token=$(oc get secret ${sa_secret_name} --template='{{index .metadata.annotations "openshift.io/token-secret.value"}}')
    registry=$(oc get svc/docker-registry -n default --template='{{.spec.clusterIP}}:{{(index .spec.ports 0).port}}')
    docker login -u aaa -p ${token} ${registry}
    is_image=${registry}/$(oc project --short)/openshift-acme
    docker tag openshift-acme-candidate ${is_image}
    docker push ${is_image}
    sleep 3
    oc rollout status deploy/openshift-acme

    make -j64 test-extended GOFLAGS="-race" GO_ET_KUBECONFIG=~/.kube/config GO_ET_DOMAIN=${domain} || (oc logs deploy/openshift-acme; false)
    oc logs deploy/openshift-acme

    oc cluster down
done
