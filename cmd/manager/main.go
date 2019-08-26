package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/openshift/cluster-api-provider-gcp/pkg/apis"
	providerspecv1 "github.com/openshift/cluster-api-provider-gcp/pkg/apis/gcpprovider/v1beta1"
	"github.com/openshift/cluster-api-provider-gcp/pkg/cloud/gcp/actuators/machine"
	"github.com/openshift/cluster-api-provider-gcp/pkg/version"
	clusterapis "github.com/openshift/cluster-api/pkg/apis"
	capimachine "github.com/openshift/cluster-api/pkg/controller/machine"
	"k8s.io/klog"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/runtime/signals"
)

func main() {
	var printVersion bool
	flag.BoolVar(&printVersion, "version", false, "print version and exit")

	klog.InitFlags(nil)
	watchNamespace := flag.String("namespace", "", "Namespace that the controller watches to reconcile machine-api objects. If unspecified, the controller watches for machine-api objects across all namespaces.")
	flag.Set("logtostderr", "true")
	flag.Parse()

	if printVersion {
		fmt.Println(version.String)
		os.Exit(0)
	}

	cfg := config.GetConfigOrDie()

	opts := manager.Options{}
	if *watchNamespace != "" {
		opts.Namespace = *watchNamespace
		klog.Infof("Watching machine-api objects only in namespace %q for reconciliation.", opts.Namespace)
	}

	// Setup a Manager
	mgr, err := manager.New(cfg, opts)
	if err != nil {
		klog.Fatalf("Failed to set up overall controller manager: %v", err)
	}

	codec, err := providerspecv1.NewCodec()
	if err != nil {
		klog.Fatalf("Unable to create codec: %v", err)
	}

	// Initialize machine actuator.
	machineActuator := machine.NewActuator(machine.ActuatorParams{
		CoreClient:    mgr.GetClient(),
		EventRecorder: mgr.GetEventRecorderFor("gcpcontroller"),
		Codec:         codec,
	})

	if err := apis.AddToScheme(mgr.GetScheme()); err != nil {
		klog.Fatal(err)
	}

	if err := clusterapis.AddToScheme(mgr.GetScheme()); err != nil {
		klog.Fatal(err)
	}

	capimachine.AddWithActuator(mgr, machineActuator)

	if err := mgr.Start(signals.SetupSignalHandler()); err != nil {
		klog.Fatalf("Failed to run manager: %v", err)
	}
}
