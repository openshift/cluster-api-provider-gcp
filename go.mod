module github.com/openshift/cluster-api-provider-gcp

go 1.12

require (
	github.com/beorn7/perks v1.0.0 // indirect
	github.com/blang/semver v3.5.0+incompatible
	github.com/go-log/log v0.0.0-20181211034820-a514cf01a3eb // indirect
	github.com/golang/groupcache v0.0.0-20190129154638-5b532d6fd5ef // indirect
	github.com/imdario/mergo v0.3.7 // indirect

	// kube-1.16.0
	github.com/openshift/cluster-api v0.0.0-20191003080455-24cfb34ea1f9
	github.com/pkg/errors v0.8.1
	github.com/prometheus/client_model v0.0.0-20190129233127-fd36f4220a90 // indirect
	github.com/prometheus/common v0.3.0 // indirect
	github.com/prometheus/procfs v0.0.0-20190503130316-740c07785007 // indirect
	golang.org/x/oauth2 v0.0.0-20190604053449-0f29369cfe45
	golang.org/x/time v0.0.0-20190308202827-9d24e82272b4 // indirect
	google.golang.org/api v0.4.0
	k8s.io/api v0.0.0-20190918155943-95b840bb6a1f
	k8s.io/apimachinery v0.0.0-20190913080033-27d36303b655
	k8s.io/client-go v0.0.0-20190918160344-1fbdaa4c8d90
	k8s.io/klog v0.4.0
	sigs.k8s.io/controller-runtime v0.2.0-beta.1.0.20190918234459-801e12a50160
	sigs.k8s.io/controller-tools v0.2.2-0.20190930215132-4752ed2de7d2
	sigs.k8s.io/yaml v1.1.0

)

replace sigs.k8s.io/controller-runtime => github.com/enxebre/controller-runtime v0.2.0-beta.1.0.20190930160522-58015f7fc885
