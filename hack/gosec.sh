#!/bin/sh
if [ "$IS_CONTAINER" != "" ]; then
  go get github.com/securego/gosec/cmd/gosec
  gosec -severity medium -confidence medium -exclude G304 -quiet "${@}"
else
  docker run --rm \
    --env GO111MODULE="$GO111MODULE" \
    --env GOFLAGS="$GOFLAGS" \
    --env GOPROXY="$GOPROXY" \
    --env IS_CONTAINER=TRUE \
    --volume "${PWD}:/go/src/github.com/openshift/cluster-api-provider-gcp:z" \
    --workdir /go/src/github.com/openshift/cluster-api-provider-gcp \
    openshift/origin-release:golang-1.13 \
    ./hack/gosec.sh "${@}"
fi;
