package machine

import (
	"fmt"

	"github.com/pkg/errors"

	"google.golang.org/api/compute/v1"

	"github.com/openshift/cluster-api-provider-gcp/pkg/apis/gcpprovider/v1beta1"
	computeservice "github.com/openshift/cluster-api-provider-gcp/pkg/cloud/gcp/actuators/services/compute"
	machinev1 "github.com/openshift/cluster-api/pkg/apis/machine/v1beta1"
	machineclient "github.com/openshift/cluster-api/pkg/client/clientset_generated/clientset/typed/machine/v1beta1"
	"k8s.io/klog"
	controllerclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// machineScopeParams defines the input parameters used to create a new MachineScope.
type machineScopeParams struct {
	machineClient machineclient.MachineV1beta1Interface
	coreClient    controllerclient.Client
	machine       *machinev1.Machine
}

// machineScope defines a scope defined around a machine and its cluster.
type machineScope struct {
	machineClient  machineclient.MachineInterface
	coreClient     controllerclient.Client
	projectID      string
	providerID     string
	computeService computeservice.GCPComputeService
	machine        *machinev1.Machine
	providerSpec   *v1beta1.GCPMachineProviderSpec
	providerStatus *v1beta1.GCPMachineProviderStatus
}

// newMachineScope creates a new MachineScope from the supplied parameters.
// This is meant to be called for each machine actuator operation.
func newMachineScope(params machineScopeParams) (*machineScope, error) {
	providerSpec, err := v1beta1.ProviderSpecFromRawExtension(params.machine.Spec.ProviderSpec.Value)
	if err != nil {
		return nil, fmt.Errorf("failed to get machine config: %v", err)
	}

	providerStatus, err := v1beta1.ProviderStatusFromRawExtension(params.machine.Status.ProviderStatus)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get machine provider status")
	}

	serviceAccountJSON, err := getCredentialsSecret(params.coreClient, *params.machine, *providerSpec)
	if err != nil {
		return nil, fmt.Errorf("failed to get serviceAccountJSON: %v", err)
	}

	projectID, err := getProjectIDFromJSONKey([]byte(serviceAccountJSON))
	if err != nil {
		return nil, fmt.Errorf("error getting project from JSON key: %v", err)
	}

	oauthClient, err := createOauth2Client(serviceAccountJSON, compute.CloudPlatformScope)
	if err != nil {
		return nil, fmt.Errorf("error creating oauth client: %v", err)
	}

	computeService, err := computeservice.NewComputeService(oauthClient)
	if err != nil {
		return nil, fmt.Errorf("error creating compute service: %v", err)
	}
	return &machineScope{
		machineClient: params.machineClient.Machines(params.machine.Namespace),
		coreClient:    params.coreClient,
		projectID:     projectID,
		// https://github.com/kubernetes/kubernetes/blob/8765fa2e48974e005ad16e65cb5c3acf5acff17b/staging/src/k8s.io/legacy-cloud-providers/gce/gce_util.go#L204
		providerID:     fmt.Sprintf("gce://%s/%s/%s", projectID, providerSpec.Zone, params.machine.Name),
		computeService: computeService,
		machine:        params.machine,
		providerSpec:   providerSpec,
		providerStatus: providerStatus,
	}, nil
}

// Close the MachineScope by persisting the machine spec, machine status after reconciling.
func (s *machineScope) Close() {
	if s.machineClient == nil {
		klog.Errorf("No machineClient is set for this scope")
		return
	}

	latestMachine, err := s.storeMachineSpec(s.machine)
	if err != nil {
		klog.Errorf("[machinescope] failed to update machine %q in namespace %q: %v", s.machine.Name, s.machine.Namespace, err)
		return
	}

	_, err = s.storeMachineStatus(latestMachine)
	if err != nil {
		klog.Errorf("[machinescope] failed to store provider status for machine %q in namespace %q: %v", s.machine.Name, s.machine.Namespace, err)
	}
}

func (s *machineScope) storeMachineSpec(machine *machinev1.Machine) (*machinev1.Machine, error) {
	ext, err := v1beta1.RawExtensionFromProviderSpec(s.providerSpec)
	if err != nil {
		return nil, err
	}

	klog.V(4).Infof("Storing machine spec for %q, resourceVersion: %v, generation: %v", s.machine.Name, s.machine.ResourceVersion, s.machine.Generation)
	machine.Spec.ProviderSpec.Value = ext
	return s.machineClient.Update(machine)
}

func (s *machineScope) storeMachineStatus(machine *machinev1.Machine) (*machinev1.Machine, error) {
	ext, err := v1beta1.RawExtensionFromProviderStatus(s.providerStatus)
	if err != nil {
		return nil, err
	}

	klog.V(4).Infof("Storing machine status for %q, resourceVersion: %v, generation: %v", s.machine.Name, s.machine.ResourceVersion, s.machine.Generation)
	s.machine.Status.DeepCopyInto(&machine.Status)
	machine.Status.ProviderStatus = ext
	return s.machineClient.UpdateStatus(machine)
}
