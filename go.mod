module github.com/openshift/cluster-api-provider-gcp

go 1.13

require (
	github.com/blang/semver v3.5.1+incompatible
	github.com/go-logr/logr v0.2.1
	github.com/moby/term v0.0.0-20200915141129-7f0af18e79f2 // indirect
	github.com/onsi/ginkgo v1.12.1
	github.com/onsi/gomega v1.10.1
	github.com/openshift/api v0.0.0-20200901182017-7ac89ba6b971
	github.com/openshift/machine-api-operator v0.2.1-0.20201002104344-6abfb5440597
	golang.org/x/oauth2 v0.0.0-20200902213428-5d25da1a8d43
	golang.org/x/sys v0.0.0-20201018230417-eeed37f84f13 // indirect
	golang.org/x/tools v0.0.0-20201020123448-f5c826d1900e // indirect
	google.golang.org/api v0.33.0

	// kube 1.18
	k8s.io/api v0.19.0
	k8s.io/apimachinery v0.19.0
	k8s.io/client-go v0.19.0
	k8s.io/klog/v2 v2.3.0
	sigs.k8s.io/controller-runtime v0.6.2
	sigs.k8s.io/controller-tools v0.3.0
	sigs.k8s.io/yaml v1.2.0
)

replace (
	sigs.k8s.io/cluster-api-provider-aws => github.com/openshift/cluster-api-provider-aws v0.2.1-0.20200929152424-eab2e087f366
	sigs.k8s.io/cluster-api-provider-azure => github.com/openshift/cluster-api-provider-azure v0.1.0-alpha.3.0.20200929220456-04e680e51d03
)
