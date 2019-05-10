package machine

import (
	"reflect"
	"testing"

	gcpv1beta1 "github.com/openshift/cluster-api-provider-gcp/pkg/apis/gcpprovider/v1beta1"
	machinev1beta1 "github.com/openshift/cluster-api/pkg/apis/machine/v1beta1"
	machineapifake "github.com/openshift/cluster-api/pkg/client/clientset_generated/clientset/fake"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	controllerfake "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestStoreMachineSpec(t *testing.T) {
	storedProviderSpec := gcpv1beta1.GCPMachineProviderSpec{
		CanIPForward: false,
	}
	storedRawExtension, err := gcpv1beta1.RawExtensionFromProviderSpec(&storedProviderSpec)
	if err != nil {
		t.Fatalf("Failed running RawExtensionFromProviderSpec: %v", err)
	}
	storedMachine := &machinev1beta1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "instance",
			Namespace: "test",
		},
		Spec: machinev1beta1.MachineSpec{
			ProviderSpec: machinev1beta1.ProviderSpec{
				Value: storedRawExtension,
			},
		},
	}

	expectedProviderSpec := storedProviderSpec.DeepCopy()
	expectedProviderSpec.CanIPForward = true
	expectedRawExtension, err := gcpv1beta1.RawExtensionFromProviderSpec(expectedProviderSpec)
	if err != nil {
		t.Fatalf("Failed running RawExtensionFromProviderSpec: %v", err)
	}
	expectedProviderID := "gce://project/zone/instance"
	expectedMachine := storedMachine.DeepCopy()
	expectedMachine.Spec.ProviderID = &expectedProviderID
	expectedMachine.Spec.ProviderSpec.Value = expectedRawExtension

	s := machineScope{
		machine:        expectedMachine,
		coreClient:     controllerfake.NewFakeClient(),
		machineClient:  machineapifake.NewSimpleClientset(storedMachine).MachineV1beta1().Machines("test"),
		providerSpec:   expectedProviderSpec,
		providerStatus: &gcpv1beta1.GCPMachineProviderStatus{},
	}

	latestMachine, err := s.storeMachineSpec(s.machine)
	if err != nil {
		t.Errorf("Failed running storeMachineSpec: %v", err)
	}
	if *latestMachine.Spec.ProviderID != expectedProviderID {
		t.Errorf("Expected: %v, got: %v", expectedProviderID, *latestMachine.Spec.ProviderID)
	}
	gotProviderSpec, err := gcpv1beta1.ProviderSpecFromRawExtension(latestMachine.Spec.ProviderSpec.Value)
	if err != nil {
		t.Errorf("Failed running ProviderSpecFromRawExtension: %v", err)
	}
	if !reflect.DeepEqual(gotProviderSpec, expectedProviderSpec) {
		t.Errorf("Expected: %+v, got: %+v", expectedProviderSpec, gotProviderSpec)
	}
}
