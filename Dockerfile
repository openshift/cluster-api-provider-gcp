FROM registry.svc.ci.openshift.org/openshift/release:golang-1.12 AS builder
WORKDIR /go/src/github.com/openshift/cluster-api-provider-gcp
COPY . .
# VERSION env gets set in the openshift/release image and refers to the golang version, which interfers with our own
RUN unset VERSION \
 && make build NO_DOCKER=1 CGO_ENABLED=0

FROM registry.svc.ci.openshift.org/openshift/origin-v4.0:base
COPY --from=builder /go/src/github.com/openshift/cluster-api-provider-gcp/bin/manager /
COPY --from=builder /go/src/github.com/openshift/cluster-api-provider-gcp/bin/machine-controller-manager /
