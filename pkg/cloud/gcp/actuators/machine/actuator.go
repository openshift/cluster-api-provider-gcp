package machine

import (
	"context"
	"fmt"

	clusterv1 "github.com/openshift/cluster-api/pkg/apis/cluster/v1alpha1"
	machinev1 "github.com/openshift/cluster-api/pkg/apis/machine/v1beta1"
	mapiclient "github.com/openshift/cluster-api/pkg/client/clientset_generated/clientset/typed/machine/v1beta1"
	"k8s.io/klog"
	controllerclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// Actuator is responsible for performing machine reconciliation.
type Actuator struct {
	machineClient mapiclient.MachineV1beta1Interface
	coreClient    controllerclient.Client
}

// ActuatorParams holds parameter information for Actuator.
type ActuatorParams struct {
	MachineClient mapiclient.MachineV1beta1Interface
	CoreClient    controllerclient.Client
}

// NewActuator returns an actuator.
func NewActuator(params ActuatorParams) *Actuator {
	return &Actuator{
		machineClient: params.MachineClient,
		coreClient:    params.CoreClient,
	}
}

// Create creates a machine and is invoked by the machine controller.
func (a *Actuator) Create(ctx context.Context, cluster *clusterv1.Cluster, machine *machinev1.Machine) error {
	klog.Infof("Creating machine %v", machine.Name)
	scope, err := newMachineScope(machineScopeParams{
		machineClient: a.machineClient,
		coreClient:    a.coreClient,
		machine:       machine,
	})
	if err != nil {
		return fmt.Errorf("failed to create scope for machine %q: %v", machine.Name, err)
	}
	// scope and reconciler lifetime is a machine actuator operation
	// when scope is closed, it will persist to etcd the given machine spec and machine status (if modified)
	defer scope.Close()
	return newReconciler(scope).create()
}

func (a *Actuator) Exists(ctx context.Context, cluster *clusterv1.Cluster, machine *machinev1.Machine) (bool, error) {
	// TODO(alberto): implement this
	return false, nil
}

func (a *Actuator) Update(ctx context.Context, cluster *clusterv1.Cluster, machine *machinev1.Machine) error {
	// TODO(alberto): implement this
	return nil
}

func (a *Actuator) Delete(ctx context.Context, cluster *clusterv1.Cluster, machine *machinev1.Machine) error {
	// TODO(alberto): implement this
	return nil
}
