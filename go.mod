module github.com/openshift/cluster-api-provider-gcp

go 1.13

require (
	github.com/blang/semver v3.5.1+incompatible
	github.com/go-logr/logr v0.1.0
	github.com/onsi/ginkgo v1.12.0
	github.com/onsi/gomega v1.8.1

	// kube-1.16.0
	github.com/openshift/machine-api-operator v0.2.1-0.20200306195511-8fff6d5a4cff
	golang.org/x/oauth2 v0.0.0-20190604053449-0f29369cfe45
	google.golang.org/api v0.4.0
	k8s.io/api v0.18.0
	k8s.io/apimachinery v0.18.0
	k8s.io/client-go v0.18.0
	k8s.io/klog v1.0.0
	sigs.k8s.io/controller-runtime v0.5.1-0.20200327213554-2d4c4877f906
	sigs.k8s.io/controller-tools v0.2.8
	sigs.k8s.io/yaml v1.2.0
)

replace github.com/openshift/machine-api-operator => github.com/joelspeed/machine-api-operator v0.2.1-0.20200417102748-367ae647375f
