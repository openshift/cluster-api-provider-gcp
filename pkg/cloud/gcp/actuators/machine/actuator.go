package machine

// This is a thin layer to implement the machine actuator interface with cloud provider details.
// The lifetime of scope and reconciler is a machine actuator operation.
// when scope is closed, it will persist to etcd the given machine spec and machine status (if modified)
import (
	"context"
	"fmt"
	"time"

	"github.com/openshift/cluster-api-provider-gcp/pkg/apis/gcpprovider/v1beta1"
	computeservice "github.com/openshift/cluster-api-provider-gcp/pkg/cloud/gcp/actuators/services/compute"
	clusterv1 "github.com/openshift/cluster-api/pkg/apis/cluster/v1alpha1"
	machinev1 "github.com/openshift/cluster-api/pkg/apis/machine/v1beta1"
	mapiclient "github.com/openshift/cluster-api/pkg/client/clientset_generated/clientset/typed/machine/v1beta1"
	clustererror "github.com/openshift/cluster-api/pkg/controller/error"
	compute "google.golang.org/api/compute/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog"
	controllerclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	credentialsSecretKey = "serviceAccountJSON"
	operationRetryWait   = 5 * time.Second
	operationTimeOut     = 180 * time.Second
	requeuePeriod        = 20 * time.Second
	userDataSecretKey    = "userData"
	createOperationKey   = "CREATE_OP_NAME_AND_PATH_TBD"
	deleteOperationKey   = "DELETE_OP_NAME_AND_PATH_TBD"
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

type machineContext struct {
	computeService computeservice.GCPComputeService
	projectID      string
	providerID     string
	providerSpec   *v1beta1.GCPMachineProviderSpec
	providerStatus *v1beta1.GCPMachineProviderStatus
	userdata       string
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
	klog.Infof("Creating machine %q", machine.Name)

	machineCtx, err := newMachineContext(a.coreClient, machine)
	if err != nil {
		return err
	}

	instance, err := instanceGet(machineCtx, machine.Name)
	if err != nil {
		return err
	}

	if instance == nil { // InstancesInsert is not idempotent
		instance = instanceFromMachineContext(machineCtx, machine.Name)
		operation, err := machineCtx.computeService.InstancesInsert(machineCtx.projectID, machineCtx.providerSpec.Zone, instance)
		if err != nil {
			return err
		}
		if instance == nil {
			klog.Infof("Instance not created, returning an error to requeue")
			return &clustererror.RequeueAfterError{RequeueAfter: requeuePeriod}
		}
		if machine.Annotations == nil {
			machine.Annotations = map[string]string{}
		}

		machine.Annotations[createOperationKey] = fmt.Sprintf("%v", operation.Id)
		machine, err = a.machineClient.Machines(machine.Namespace).Update(machine) // needs retry on failure
		if err != nil {
			return err
		}
		klog.Infof("Created instance %s/%s/%s. Operation id #%v", machineCtx.projectID, machineCtx.providerSpec.Zone, machine.Name, operation.Id)
		return &clustererror.RequeueAfterError{RequeueAfter: requeuePeriod}
	}

	klog.Infof("Creating machine %q, status: %s", machine.Name, instance.Status)

	if val, ok := machine.Annotations[createOperationKey]; !ok || val == "" {
		klog.Infof("machine has no create operation annotation, deleting instance and returning an error to requeue")
		if _, err = machineCtx.computeService.InstancesDelete(machineCtx.projectID, machineCtx.providerSpec.Zone, machine.Name); err != nil {
			return err
		}
		return &clustererror.RequeueAfterError{RequeueAfter: requeuePeriod}
	}

	if err := a.populateMachineWithInstanceState(machine, machineCtx, instance); err != nil {
		return err
	}

	if _, err := a.updateMachineStatus(machine, machineCtx.providerStatus); err != nil {
		return err
	}

	op, err := machineCtx.computeService.ZoneOperationsGet(machineCtx.projectID, machineCtx.providerSpec.Zone, machine.Annotations[createOperationKey])
	if err != nil {
		return err
	}

	if op.Status != "DONE" {
		klog.Infof("Instance state not running, returning an error to requeue")
		return &clustererror.RequeueAfterError{RequeueAfter: requeuePeriod}
	}

	return nil
}

func (a *Actuator) Exists(ctx context.Context, cluster *clusterv1.Cluster, machine *machinev1.Machine) (bool, error) {
	klog.Infof("Checking if machine %q exists", machine.Name)

	machineCtx, err := newMachineContext(a.coreClient, machine)
	if err != nil {
		return false, err
	}

	if err := a.validateProviderSpec(machineCtx.providerSpec); err != nil {
		return false, fmt.Errorf("failed validating machine provider spec: %v", err)
	}

	op, _ := machineCtx.computeService.ZoneOperationsGet(machineCtx.projectID, machineCtx.providerSpec.Zone, machine.Annotations[createOperationKey])
	return op != nil && op.Status == "DONE", nil
}

func (a *Actuator) Update(ctx context.Context, cluster *clusterv1.Cluster, machine *machinev1.Machine) error {
	klog.Infof("Updating machine %q", machine.Name)

	machineCtx, err := newMachineContext(a.coreClient, machine)
	if err != nil {
		return err
	}

	instance, err := instanceGet(machineCtx, machine.Name)
	if err != nil {
		return fmt.Errorf("failed to get instance via compute service: %v", err)
	}

	if err := a.populateMachineWithInstanceState(machine, machineCtx, instance); err != nil {
		return err
	}

	if _, err = a.updateMachineStatus(machine, machineCtx.providerStatus); err != nil {
		return err
	}

	_, err = a.updateMachineSpec(machine, machineCtx.providerSpec)
	return err
}

func (a *Actuator) Delete(ctx context.Context, cluster *clusterv1.Cluster, machine *machinev1.Machine) error {
	klog.Infof("Deleting machine %v", machine.Name)

	machineCtx, err := newMachineContext(a.coreClient, machine)
	if err != nil {
		return err
	}

	instance, err := instanceGet(machineCtx, machine.Name)
	if err != nil {
		return err
	}

	if instance == nil {
		klog.Infof("Machine %v not found during delete, skipping", machine.Name)
		return nil
	}

	klog.Infof("Deleting machine %q, status %q", machine.Name, instance.Status)

	val, ok := machine.Annotations[deleteOperationKey]
	if !ok || val == "" {
		operation, err := machineCtx.computeService.InstancesDelete(machineCtx.projectID, machineCtx.providerSpec.Zone, machine.Name)
		if err != nil {
			return err
		}
		if machine.Annotations == nil {
			machine.Annotations = map[string]string{}
		}
		machine.Annotations[deleteOperationKey] = fmt.Sprintf("%v", operation.Id)
		machine, err = a.machineClient.Machines(machine.Namespace).Update(machine) // needs retry on failure
		if err != nil {
			return err
		}
		klog.Infof("Deleted instance %s/%s/%s. Operation id #%v", machineCtx.projectID, machineCtx.providerSpec.Zone, machine.Name, operation.Id)
		return &clustererror.RequeueAfterError{RequeueAfter: requeuePeriod}
	}

	op, err := machineCtx.computeService.ZoneOperationsGet(machineCtx.projectID, machineCtx.providerSpec.Zone, machine.Annotations[deleteOperationKey])
	if err != nil {
		return err
	}

	if op.Status != "DONE" {
		klog.Infof("Instance not terminated, returning an error to requeue")
		return &clustererror.RequeueAfterError{RequeueAfter: requeuePeriod}
	}

	return nil
}

func (a *Actuator) updateMachineStatus(machine *machinev1.Machine, providerStatus *v1beta1.GCPMachineProviderStatus) (*machinev1.Machine, error) {
	ext, err := v1beta1.RawExtensionFromProviderStatus(providerStatus)
	if err != nil {
		return nil, err
	}

	klog.V(4).Infof("Storing machine status for %q, resourceVersion: %v, generation: %v", machine.Name, machine.ResourceVersion, machine.Generation)
	machine.Status.DeepCopyInto(&machine.Status)
	machine.Status.ProviderStatus = ext
	return a.machineClient.Machines(machine.Namespace).UpdateStatus(machine)
}

func (a *Actuator) updateMachineSpec(machine *machinev1.Machine, providerSpec *v1beta1.GCPMachineProviderSpec) (*machinev1.Machine, error) {
	ext, err := v1beta1.RawExtensionFromProviderSpec(providerSpec)
	if err != nil {
		return nil, err
	}

	klog.V(4).Infof("Storing machine spec for %q, resourceVersion: %v, generation: %v", machine.Name, machine.ResourceVersion, machine.Generation)
	machine.Spec.ProviderSpec.Value = ext
	return a.machineClient.Machines(machine.Namespace).Update(machine)
}

func (a *Actuator) populateMachineWithInstanceState(machine *machinev1.Machine, machineCtx *machineContext, instance *compute.Instance) error {
	klog.Infof("Reconciling machine object %q with cloud state", machine.Name)

	if len(instance.NetworkInterfaces) < 1 {
		return fmt.Errorf("could not find network interfaces for instance %q", instance.Name)
	}

	networkInterface := instance.NetworkInterfaces[0]
	nodeAddresses := []corev1.NodeAddress{{Type: corev1.NodeInternalIP, Address: networkInterface.NetworkIP}}
	for _, config := range networkInterface.AccessConfigs {
		nodeAddresses = append(nodeAddresses, corev1.NodeAddress{Type: corev1.NodeExternalIP, Address: config.NatIP})
	}

	machine.Spec.ProviderID = &machineCtx.providerID
	machine.Status.Addresses = nodeAddresses

	machineCtx.providerStatus.InstanceState = &instance.Status
	machineCtx.providerStatus.InstanceID = &instance.Name

	machineCtx.providerStatus.Conditions = reconcileProviderConditions(machineCtx.providerStatus.Conditions, v1beta1.GCPMachineProviderCondition{
		Type:    v1beta1.MachineCreated,
		Reason:  machineCreationSucceedReason,
		Message: machineCreationSucceedMessage,
		Status:  corev1.ConditionTrue,
	})

	return nil
}

func (a *Actuator) validateProviderSpec(providerSpec *v1beta1.GCPMachineProviderSpec) error {
	// TODO(alberto): First validation should happen via webhook
	// before the object is persisted. This is a complementary
	// validation to fail early in case of lacking proper webhook
	// validation. Default values can also be set here.
	return nil
}
