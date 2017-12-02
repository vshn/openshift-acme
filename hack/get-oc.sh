#!/bin/bash

target_path=${1:-./}


curl -L https://github.com/openshift/origin/releases/download/v3.6.1/openshift-origin-client-tools-v3.6.1-008f2d5-linux-64bit.tar.gz | tar -f - -x -z -k --strip-components=1 --wildcards '*/oc' --transform='s|oc|oc-v3.6.1|' -C ${target_path}

curl -L https://github.com/openshift/origin/releases/download/v3.7.0/openshift-origin-client-tools-v3.7.0-7ed6862-linux-64bit.tar.gz | tar -f - -x -z -k --strip-components=1 --wildcards '*/oc' --transform='s|oc|oc-v3.7.0|' -C ${target_path}
