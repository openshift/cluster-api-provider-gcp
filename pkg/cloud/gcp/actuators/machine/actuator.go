package machine

// This is a thin layer to implement the machine actuator interface with cloud provider details.
// The lifetime of scope and reconciler is a machine actuator operation.
// when scope is closed, it will persist to etcd the given machine spec and machine status (if modified)
import (
	"context"
	"fmt"

	providerconfig "github.com/openshift/cluster-api-provider-gcp/pkg/apis/gcpprovider/v1beta1"
	clusterv1 "github.com/openshift/cluster-api/pkg/apis/cluster/v1alpha1"
	machinev1 "github.com/openshift/cluster-api/pkg/apis/machine/v1beta1"
	mapiclient "github.com/openshift/cluster-api/pkg/client/clientset_generated/clientset/typed/machine/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog"
	controllerclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	scopeFailFmt      = "%s: failed to create scope for machine: %v"
	createEventAction = "Create"
	updateEventAction = "Update"
	deleteEventAction = "Delete"
	noEventAction     = ""
)

// Actuator is responsible for performing machine reconciliation.
type Actuator struct {
	machineClient mapiclient.MachineV1beta1Interface
	coreClient    controllerclient.Client
	eventRecorder record.EventRecorder
	codec         *providerconfig.GCPProviderConfigCodec
}

// ActuatorParams holds parameter information for Actuator.
type ActuatorParams struct {
	MachineClient mapiclient.MachineV1beta1Interface
	CoreClient    controllerclient.Client
	EventRecorder record.EventRecorder
	Codec         *providerconfig.GCPProviderConfigCodec
}

// NewActuator returns an actuator.
func NewActuator(params ActuatorParams) *Actuator {
	return &Actuator{
		machineClient: params.MachineClient,
		coreClient:    params.CoreClient,
		eventRecorder: params.EventRecorder,
		codec:         params.Codec,
	}
}

// Set corresponding event based on error. It also returns the original error
// for convenience, so callers can do "return handleMachineError(...)".
func (a *Actuator) handleMachineError(machine *machinev1.Machine, err error, eventAction string) error {
	if eventAction != noEventAction {
		a.eventRecorder.Eventf(machine, corev1.EventTypeWarning, "Failed"+eventAction, "%v", err)
	}

	return err
}

// Create creates a machine and is invoked by the machine controller.
func (a *Actuator) Create(ctx context.Context, cluster *clusterv1.Cluster, machine *machinev1.Machine) error {
	klog.Infof("%s: Creating machine", machine.Name)
	scope, err := newMachineScope(machineScopeParams{
		machineClient: a.machineClient,
		coreClient:    a.coreClient,
		machine:       machine,
	})
	if err != nil {
		fmtErr := fmt.Sprintf(scopeFailFmt, machine.Name, err)
		return a.handleMachineError(machine, fmt.Errorf(fmtErr), createEventAction)
	}
	if err := newReconciler(scope).create(); err != nil {
		return a.handleMachineError(machine, err, createEventAction)
	}
	a.eventRecorder.Eventf(machine, corev1.EventTypeNormal, createEventAction, "Created Machine %v", machine.Name)
	return scope.Close()
}

func (a *Actuator) Exists(ctx context.Context, cluster *clusterv1.Cluster, machine *machinev1.Machine) (bool, error) {
	klog.Infof("%s: Checking if machine exists", machine.Name)
	scope, err := newMachineScope(machineScopeParams{
		machineClient: a.machineClient,
		coreClient:    a.coreClient,
		machine:       machine,
	})
	if err != nil {
		return false, fmt.Errorf(scopeFailFmt, machine.Name, err)
	}
	// The core machine controller calls exists() + create()/update() in the same reconciling operation.
	// If exists() would store machineSpec/status object then create()/update() would still receive the local version.
	// When create()/update() try to store machineSpec/status this might result in
	// "Operation cannot be fulfilled; the object has been modified; please apply your changes to the latest version and try again."
	// Therefore we don't close the scope here and we only store spec/status atomically either in create()/update()"
	return newReconciler(scope).exists()
}

func (a *Actuator) Update(ctx context.Context, cluster *clusterv1.Cluster, machine *machinev1.Machine) error {
	klog.Infof("%s: Updating machine", machine.Name)
	scope, err := newMachineScope(machineScopeParams{
		machineClient: a.machineClient,
		coreClient:    a.coreClient,
		machine:       machine,
	})
	if err != nil {
		fmtErr := fmt.Sprintf(scopeFailFmt, machine.Name, err)
		return a.handleMachineError(machine, fmt.Errorf(fmtErr), updateEventAction)
	}
	if err := newReconciler(scope).update(); err != nil {
		return a.handleMachineError(machine, err, updateEventAction)
	}
	a.eventRecorder.Eventf(machine, corev1.EventTypeNormal, updateEventAction, "Updated Machine %v", machine.Name)
	return scope.Close()
}

func (a *Actuator) Delete(ctx context.Context, cluster *clusterv1.Cluster, machine *machinev1.Machine) error {
	klog.Infof("%s: Deleting machine", machine.Name)
	scope, err := newMachineScope(machineScopeParams{
		machineClient: a.machineClient,
		coreClient:    a.coreClient,
		machine:       machine,
	})
	if err != nil {
		fmtErr := fmt.Sprintf(scopeFailFmt, machine.Name, err)
		return a.handleMachineError(machine, fmt.Errorf(fmtErr), deleteEventAction)
	}
	if err := newReconciler(scope).delete(); err != nil {
		return a.handleMachineError(machine, err, deleteEventAction)
	}
	a.eventRecorder.Eventf(machine, corev1.EventTypeNormal, deleteEventAction, "Deleted machine %v", machine.Name)
	return nil
}
