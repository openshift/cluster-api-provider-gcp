TOOLS_DIR := ../hack/tools
TOOLS_BIN_DIR := $(abspath $(TOOLS_DIR)/bin)
KUSTOMIZE_VER := v3.8.6
KUSTOMIZE_BIN := kustomize
KUSTOMIZE := $(TOOLS_BIN_DIR)/$(KUSTOMIZE_BIN)-$(KUSTOMIZE_VER)
GO_INSTALL = ../scripts/go_install.sh

$(KUSTOMIZE): ## Build kustomize from tools folder.
	GOBIN=$(TOOLS_BIN_DIR) $(GO_INSTALL) sigs.k8s.io/kustomize/kustomize/v3 $(KUSTOMIZE_BIN) $(KUSTOMIZE_VER)

.PHONY: ocp-manifests
ocp-manifests:  $(KUSTOMIZE)  ## Builds openshift specific manifests
	$(KUSTOMIZE) build ../config/default > infrastructure-components.yaml
