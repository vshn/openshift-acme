#!/bin/bash
set -e
shopt -s expand_aliases

wd=$(pwd)

script_full_path=$(readlink -f $0)
script_dir=$(dirname ${script_full_path})
pushd ${script_dir}/..

if [[ "$1" = /* ]]
then
   # Absolute path
   bindir=$1
else
   # Relative path
   bindir=${wd}/${1:-.}
fi
PATH=${bindir}:${PATH}
prefix=${bindir}/oc-
binaries=$(echo ${prefix}*)
echo binaries: ${binaries}

[[ ! -z "${binaries}" ]]

make -j64 test-extended GOFLAGS='-i -race' TEST_FLAGS=''

function setupClusterWide() {
    tmpdir=$(mktemp -d)
    cp -r deploy/letsencrypt-staging/cluster-wide/* ${tmpdir}/
    sed -i 's/scheduled: true/scheduled: false/' ${tmpdir}/imagestream.yaml
    oc create -f${tmpdir}/{clusterrole,serviceaccount,imagestream,deployment}.yaml
    oc adm policy add-cluster-role-to-user openshift-acme -z openshift-acme
    export FIXED_NAMESPACE=""
}

function setupSingleNamespace() {
    tmpdir=$(mktemp -d)
    cp -r deploy/letsencrypt-staging/single-namespace/* ${tmpdir}/
    sed -i 's/scheduled: true/scheduled: false/' ${tmpdir}/imagestream.yaml
    oc create -f${tmpdir}/{role,serviceaccount,imagestream,deployment}.yaml
    oc policy add-role-to-user openshift-acme --role-namespace="$(oc project --short)" -z default
    export FIXED_NAMESPACE=$(oc project --short)
}

for binary in ${binaries}; do
    version=${binary#$prefix}
    echo binary version: ${version}
    ln -sfn ${binary} ${bindir}/oc
    oc version
#    for setup in {setupClusterWide,setupSingleNamespace}; do
    for setup in "setupSingleNamespace"; do
        echo ${setup}
        oc cluster up --version=${version} --server-loglevel=4
        oc login -u system:admin

        oc get all -n default

        # Wait for docker-registry
        # Wait for router
        (timeout 5m bash -c 'oc rollout status -n default dc/docker-registry && oc rollout status -n default dc/router') || (\
        oc get all -n default; \
        oc get -n default po/docker-registry-1-deploy po/router-1-deploy -o yaml; \
        oc get nodes; \
        docker logs origin; \
        sleep 3 \
        false)

        oc new-project acme-aaa
        oc get sa,secret

        ${setup}

        sa_secret_name=$(oc get sa builder --template='{{ (index .imagePullSecrets 0).name }}')
        token=$(oc get secret ${sa_secret_name} --template='{{index .metadata.annotations "openshift.io/token-secret.value"}}')
        registry=$(oc get svc/docker-registry -n default --template='{{.spec.clusterIP}}:{{(index .spec.ports 0).port}}')
        docker login -u aaa -p ${token} ${registry}
        is_image=${registry}/$(oc project --short)/openshift-acme
        docker tag openshift-acme-candidate ${is_image}
        docker push ${is_image}

        oc get all
        oc rollout status deploy/openshift-acme

        make -j64 test-extended GOFLAGS="-race" GO_ET_KUBECONFIG=~/.kube/config GO_ET_DOMAIN=${DOMAIN} || (oc logs deploy/openshift-acme; false)
        oc logs deploy/openshift-acme

        oc get deploy/openshift-acme --template='deployed: {{(index .spec.template.spec.containers 0).image}}'
        docker images

        oc cluster down
    done
done
