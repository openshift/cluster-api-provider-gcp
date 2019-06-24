#!/bin/sh
if [ "$IS_CONTAINER" != "" ]; then
  go vet "${@}"
else
  docker run --rm \
    --env IS_CONTAINER=TRUE \
    --volume "${PWD}:/go/src/github.com/openshift/cluster-api-provider-gcp:z" \
    --workdir /go/src/github.com/openshift/cluster-api-provider-gcp \
    openshift/origin-release:golang-1.12 \
    ./hack/go-vet.sh "${@}"
fi;
