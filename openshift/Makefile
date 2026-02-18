BIN_DIR := bin
TOOLS_DIR := tools

include provider-version.mk

$(RELEASE_DIR):
	mkdir -p $(RELEASE_DIR)/

MANIFESTS_GEN := go run ./vendor/github.com/openshift/cluster-capi-operator/manifests-gen/

.PHONY: verify
verify: verify-ocp-manifests

.PHONY: verify-%
verify-%: ## Ensure no diff after running some other target
	$(MAKE) $*
	./verify-diff.sh

.PHONY: update-manifests-gen
update-manifests-gen:
	cd tools && go get github.com/openshift/cluster-capi-operator/manifests-gen@main github.com/openshift/cluster-capi-operator@main && go mod tidy && go mod vendor

.PHONY: ocp-manifests
ocp-manifests: ## Builds openshift specific manifests
	cd tools && $(MANIFESTS_GEN) \
		--manifests-path "../capi-operator-manifests" \
		--kustomize-dir "../../openshift" \
		--name cluster-api-provider-gcp \
		--install-order 20 \
		--attribute type=infrastructure \
		--attribute version="${PROVIDER_VERSION}" \
		--self-image-ref registry.ci.openshift.org/openshift:gcp-cluster-api-controllers \
		--platform GCP \
		--protect-cluster-resource gcpcluster
