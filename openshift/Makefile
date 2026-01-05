BIN_DIR := bin
TOOLS_DIR := tools

include provider-version.mk

$(RELEASE_DIR):
	mkdir -p $(RELEASE_DIR)/

MANIFESTS_GEN := go run ./vendor/github.com/openshift/cluster-capi-operator/manifests-gen/

.PHONY: update-manifests-gen
update-manifests-gen:
	cd tools && go get github.com/openshift/cluster-capi-operator/manifests-gen && go mod tidy && go mod vendor

.PHONY: ocp-manifests
ocp-manifests: | $(RELEASE_DIR) ## Builds openshift specific manifests.
	# Generate provider manifests.
	cd tools && $(MANIFESTS_GEN) --base-path "../../" --manifests-path "../capi-operator-manifests" --kustomize-dir="openshift" \
		--provider-name cluster-api-provider-gcp \
		--provider-type infrastructure \
		--provider-version "${PROVIDER_VERSION}" \
		--provider-image-ref registry.ci.openshift.org/openshift:gcp-cluster-api-controllers \
		--platform GCP \
		--protect-cluster-resource gcpcluster
