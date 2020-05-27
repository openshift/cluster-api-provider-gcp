module github.com/openshift/cluster-api-provider-gcp

go 1.13

require (
	github.com/blang/semver v3.5.1+incompatible
	github.com/go-logr/logr v0.1.0
	github.com/onsi/ginkgo v1.12.0
	github.com/onsi/gomega v1.8.1
	github.com/openshift/machine-api-operator v0.2.1-0.20200527204437-14e5e0c7d862
	golang.org/x/oauth2 v0.0.0-20190604053449-0f29369cfe45
	google.golang.org/api v0.4.0

	// kube 1.18
	k8s.io/api v0.18.2
	k8s.io/apimachinery v0.18.2
	k8s.io/client-go v0.18.2
	k8s.io/klog v1.0.0
	k8s.io/utils v0.0.0-20200327001022-6496210b90e8 // indirect
	sigs.k8s.io/controller-runtime v0.5.1-0.20200330174416-a11a908d91e0
	sigs.k8s.io/controller-tools v0.2.9-0.20200331153640-3c5446d407dd
	sigs.k8s.io/yaml v1.2.0
)
