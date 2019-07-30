#!/bin/sh
if [ "$IS_CONTAINER" != "" ]; then
  go get github.com/securego/gosec/cmd/gosec
  gosec -severity medium -confidence medium -exclude G304 -quiet "${@}"
else
  docker run --rm \
    --env IS_CONTAINER=TRUE \
    --volume "${PWD}:/go/src/github.com/openshift/cluster-api-provider-gcp:z" \
    --workdir /go/src/github.com/openshift/cluster-api-provider-gcp \
    openshift/origin-release:golang-1.12 \
    ./hack/gosec.sh "${@}"
fi;
