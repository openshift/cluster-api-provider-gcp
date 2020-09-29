package machine

import (
	"context"
	"fmt"

	"github.com/openshift/cluster-api-provider-gcp/pkg/apis/gcpprovider/v1beta1"
	computeservice "github.com/openshift/cluster-api-provider-gcp/pkg/cloud/gcp/actuators/services/compute"
	"github.com/openshift/cluster-api-provider-gcp/pkg/cloud/gcp/actuators/util"
	machinev1 "github.com/openshift/machine-api-operator/pkg/apis/machine/v1beta1"
	machineapierros "github.com/openshift/machine-api-operator/pkg/controller/machine"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
	controllerclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// machineScopeParams defines the input parameters used to create a new MachineScope.
type machineScopeParams struct {
	context.Context

	coreClient           controllerclient.Client
	machine              *machinev1.Machine
	computeClientBuilder computeservice.BuilderFuncType
}

// machineScope defines a scope defined around a machine and its cluster.
type machineScope struct {
	context.Context

	coreClient     controllerclient.Client
	projectID      string
	providerID     string
	computeService computeservice.GCPComputeService
	machine        *machinev1.Machine
	providerSpec   *v1beta1.GCPMachineProviderSpec
	providerStatus *v1beta1.GCPMachineProviderStatus

	// origMachine captures original value of machine before it is updated (to
	// skip object updated if nothing is changed)
	origMachine *machinev1.Machine
	// origProviderStatus captures original value of machine provider status
	// before it is updated (to skip object updated if nothing is changed)
	origProviderStatus *v1beta1.GCPMachineProviderStatus

	machineToBePatched controllerclient.Patch
}

// newMachineScope creates a new MachineScope from the supplied parameters.
// This is meant to be called for each machine actuator operation.
func newMachineScope(params machineScopeParams) (*machineScope, error) {
	if params.Context == nil {
		params.Context = context.Background()
	}

	providerSpec, err := v1beta1.ProviderSpecFromRawExtension(params.machine.Spec.ProviderSpec.Value)
	if err != nil {
		return nil, machineapierros.InvalidMachineConfiguration("failed to get machine config: %v", err)
	}

	providerStatus, err := v1beta1.ProviderStatusFromRawExtension(params.machine.Status.ProviderStatus)
	if err != nil {
		return nil, machineapierros.InvalidMachineConfiguration("failed to get machine provider status: %v", err.Error())
	}

	serviceAccountJSON, err := util.GetCredentialsSecret(params.coreClient, params.machine.GetNamespace(), *providerSpec)
	if err != nil {
		return nil, err
	}

	projectID := providerSpec.ProjectID
	if len(projectID) == 0 {
		projectID, err = util.GetProjectIDFromJSONKey([]byte(serviceAccountJSON))
		if err != nil {
			return nil, machineapierros.InvalidMachineConfiguration("error getting project from JSON key: %v", err)
		}
	}

	computeService, err := params.computeClientBuilder(serviceAccountJSON)
	if err != nil {
		return nil, machineapierros.InvalidMachineConfiguration("error creating compute service: %v", err)
	}
	return &machineScope{
		Context:    params.Context,
		coreClient: params.coreClient,
		projectID:  projectID,
		// https://github.com/kubernetes/kubernetes/blob/8765fa2e48974e005ad16e65cb5c3acf5acff17b/staging/src/k8s.io/legacy-cloud-providers/gce/gce_util.go#L204
		providerID:     fmt.Sprintf("gce://%s/%s/%s", projectID, providerSpec.Zone, params.machine.Name),
		computeService: computeService,
		// Deep copy the machine since it is changed outside
		// of the machine scope by consumers of the machine
		// scope (e.g. reconciler).
		machine:        params.machine.DeepCopy(),
		providerSpec:   providerSpec,
		providerStatus: providerStatus,
		// Once set, they can not be changed. Otherwise, status change computation
		// might be invalid and result in skipping the status update.
		origMachine:        params.machine.DeepCopy(),
		origProviderStatus: providerStatus.DeepCopy(),
		machineToBePatched: controllerclient.MergeFrom(params.machine.DeepCopy()),
	}, nil
}

// Close the MachineScope by persisting the machine spec, machine status after reconciling.
func (s *machineScope) Close() error {
	// The machine status needs to be updated first since
	// the next call to storeMachineSpec updates entire machine
	// object. If done in the reverse order, the machine status
	// could be updated without setting the LastUpdated field
	// in the machine status. The following might occur:
	// 1. machine object is updated (including its status)
	// 2. the machine object is updated by different component/user meantime
	// 3. storeMachineStatus is called but fails since the machine object
	//    is outdated. The operation is reconciled but given the status
	//    was already set in the previous call, the status is no longer updated
	//    since the status updated condition is already false. Thus,
	//    the LastUpdated is not set/updated properly.
	if err := s.setMachineStatus(); err != nil {
		return fmt.Errorf("[machinescope] failed to set provider status for machine %q in namespace %q: %v", s.machine.Name, s.machine.Namespace, err)
	}

	if err := s.setMachineSpec(); err != nil {
		return fmt.Errorf("[machinescope] failed to set machine spec %q in namespace %q: %v", s.machine.Name, s.machine.Namespace, err)
	}

	if err := s.PatchMachine(); err != nil {
		return fmt.Errorf("[machinescope] failed to patch machine %q in namespace %q: %v", s.machine.Name, s.machine.Namespace, err)
	}

	return nil
}

func (s *machineScope) setMachineSpec() error {
	ext, err := v1beta1.RawExtensionFromProviderSpec(s.providerSpec)
	if err != nil {
		return err
	}

	klog.V(4).Infof("Storing machine spec for %q, resourceVersion: %v, generation: %v", s.machine.Name, s.machine.ResourceVersion, s.machine.Generation)
	s.machine.Spec.ProviderSpec.Value = ext

	return nil
}

func (s *machineScope) setMachineStatus() error {
	if equality.Semantic.DeepEqual(s.providerStatus, s.origProviderStatus) && equality.Semantic.DeepEqual(s.machine.Status.Addresses, s.origMachine.Status.Addresses) {
		klog.Infof("%s: status unchanged", s.machine.Name)
		return nil
	}

	klog.V(4).Infof("Storing machine status for %q, resourceVersion: %v, generation: %v", s.machine.Name, s.machine.ResourceVersion, s.machine.Generation)
	ext, err := v1beta1.RawExtensionFromProviderStatus(s.providerStatus)
	if err != nil {
		return err
	}

	s.machine.Status.ProviderStatus = ext
	time := metav1.Now()
	s.machine.Status.LastUpdated = &time

	return nil
}

func (s *machineScope) PatchMachine() error {
	klog.V(3).Infof("%q: patching machine", s.machine.GetName())

	statusCopy := *s.machine.Status.DeepCopy()

	// patch machine
	if err := s.coreClient.Patch(s.Context, s.machine, s.machineToBePatched); err != nil {
		klog.Errorf("Failed to patch machine %q: %v", s.machine.GetName(), err)
		return err
	}

	s.machine.Status = statusCopy

	// patch status
	if err := s.coreClient.Status().Patch(s.Context, s.machine, s.machineToBePatched); err != nil {
		klog.Errorf("Failed to patch machine status %q: %v", s.machine.GetName(), err)
		return err
	}

	return nil
}
