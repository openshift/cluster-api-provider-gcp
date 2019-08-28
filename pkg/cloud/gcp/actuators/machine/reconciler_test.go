package machine

import (
	"context"
	"fmt"
	"testing"

	gcpv1beta1 "github.com/openshift/cluster-api-provider-gcp/pkg/apis/gcpprovider/v1beta1"
	computeservice "github.com/openshift/cluster-api-provider-gcp/pkg/cloud/gcp/actuators/services/compute"
	machinev1beta1 "github.com/openshift/cluster-api/pkg/apis/machine/v1beta1"
	capifake "github.com/openshift/cluster-api/pkg/client/clientset_generated/clientset/fake"
	controllerError "github.com/openshift/cluster-api/pkg/controller/error"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	controllerfake "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestCreate(t *testing.T) {
	_, mockComputeService := computeservice.NewComputeServiceMock()
	machineScope := machineScope{
		machine: &machinev1beta1.Machine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "",
				Namespace: "",
			},
		},
		coreClient:     controllerfake.NewFakeClient(),
		providerSpec:   &gcpv1beta1.GCPMachineProviderSpec{},
		providerStatus: &gcpv1beta1.GCPMachineProviderStatus{},
		computeService: mockComputeService,
	}
	reconciler := newReconciler(&machineScope)
	if _, err := reconciler.create(); err != nil {
		t.Errorf("reconciler was not expected to return error: %v", err)
	}
	if reconciler.providerStatus.Conditions[0].Type != gcpv1beta1.MachineCreated {
		t.Errorf("Expected: %s, got %s", gcpv1beta1.MachineCreated, reconciler.providerStatus.Conditions[0].Type)
	}
	if reconciler.providerStatus.Conditions[0].Status != corev1.ConditionTrue {
		t.Errorf("Expected: %s, got %s", corev1.ConditionTrue, reconciler.providerStatus.Conditions[0].Status)
	}
	if reconciler.providerStatus.Conditions[0].Reason != machineCreationSucceedReason {
		t.Errorf("Expected: %s, got %s", machineCreationSucceedReason, reconciler.providerStatus.Conditions[0].Reason)
	}
	if reconciler.providerStatus.Conditions[0].Message != machineCreationSucceedMessage {
		t.Errorf("Expected: %s, got %s", machineCreationSucceedMessage, reconciler.providerStatus.Conditions[0].Message)
	}
}

func TestReconcileMachineWithCloudState(t *testing.T) {
	_, mockComputeService := computeservice.NewComputeServiceMock()

	codec, err := gcpv1beta1.NewCodec()
	if err != nil {
		t.Fatalf("Unable to create codec: %v", err)
	}

	zone := "us-east1-b"
	machineProviderSpec := &gcpv1beta1.GCPMachineProviderSpec{
		Zone: zone,
	}
	projectID := "testProject"
	instanceName := "testInstance"
	providerID := fmt.Sprintf("gce://%s/%s/%s", projectID, zone, instanceName)

	providerSpec, err := codec.EncodeProviderSpec(machineProviderSpec)
	if err != nil {
		t.Fatalf("Unable to encode provider spec: %v", err)
	}

	machine := &machinev1beta1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      instanceName,
			Namespace: "",
		},
		Spec: machinev1beta1.MachineSpec{
			ProviderSpec: *providerSpec,
		},
	}

	cs := capifake.NewSimpleClientset(machine)
	actuator := NewActuator(ActuatorParams{
		MachineClient: cs.MachineV1beta1(),
		CoreClient:    fake.NewFakeClient(machine),
		EventRecorder: &record.FakeRecorder{
			Events: make(chan string, 1),
		},
		Codec: codec,
	})

	machineScope := machineScope{
		machine:        machine,
		coreClient:     controllerfake.NewFakeClient(),
		providerSpec:   machineProviderSpec,
		projectID:      projectID,
		providerStatus: &gcpv1beta1.GCPMachineProviderStatus{},
		computeService: mockComputeService,
	}

	expectedNodeAddresses := []corev1.NodeAddress{
		{
			Type:    "InternalIP",
			Address: "10.0.0.15",
		},
		{
			Type:    "ExternalIP",
			Address: "35.243.147.143",
		},
	}

	r := newReconciler(&machineScope)
	instance, err := r.reconcileMachineWithCloudState(nil)
	if err != nil {
		t.Errorf("reconciler was not expected to return error: %v", err)
	}

	machineMod, err := actuator.updateStatus(context.TODO(), machineScope.machine, r, instance)
	if err != nil {
		t.Fatalf("Unable to update machine status: %v", err)
	}
	machine = machineMod

	if machine.Status.Addresses[0] != expectedNodeAddresses[0] {
		t.Errorf("Expected: %s, got: %s", expectedNodeAddresses[0], r.machine.Status.Addresses[0])
	}
	if machine.Status.Addresses[1] != expectedNodeAddresses[1] {
		t.Errorf("Expected: %s, got: %s", expectedNodeAddresses[1], r.machine.Status.Addresses[1])
	}

	machineMod, err = actuator.updateProviderID(machine, projectID)
	if err != nil {
		t.Fatalf("Unable to update machine status: %v", err)
	}
	machine = machineMod

	if providerID != *machine.Spec.ProviderID {
		t.Errorf("Expected: %s, got: %s", providerID, *machine.Spec.ProviderID)
	}
	if *r.providerStatus.InstanceState != "RUNNING" {
		t.Errorf("Expected: %s, got: %s", "RUNNING", *r.providerStatus.InstanceState)
	}
	if *r.providerStatus.InstanceID != instanceName {
		t.Errorf("Expected: %s, got: %s", instanceName, *r.providerStatus.InstanceID)
	}
}

func TestExists(t *testing.T) {
	_, mockComputeService := computeservice.NewComputeServiceMock()
	machineScope := machineScope{
		machine: &machinev1beta1.Machine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "",
				Namespace: "",
			},
		},
		coreClient:     controllerfake.NewFakeClient(),
		providerSpec:   &gcpv1beta1.GCPMachineProviderSpec{},
		providerStatus: &gcpv1beta1.GCPMachineProviderStatus{},
		computeService: mockComputeService,
	}
	reconciler := newReconciler(&machineScope)
	exists, err := reconciler.exists()
	if err != nil || exists != true {
		t.Errorf("reconciler was not expected to return error: %v", err)
	}
}

func TestDelete(t *testing.T) {
	_, mockComputeService := computeservice.NewComputeServiceMock()
	machineScope := machineScope{
		machine: &machinev1beta1.Machine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "",
				Namespace: "",
			},
		},
		coreClient:     controllerfake.NewFakeClient(),
		providerSpec:   &gcpv1beta1.GCPMachineProviderSpec{},
		providerStatus: &gcpv1beta1.GCPMachineProviderStatus{},
		computeService: mockComputeService,
	}
	reconciler := newReconciler(&machineScope)
	if err := reconciler.delete(); err != nil {
		if _, ok := err.(*controllerError.RequeueAfterError); !ok {
			t.Errorf("reconciler was not expected to return error: %v", err)
		}
	}
}

func TestFmtInstanceSelfLink(t *testing.T) {
	expected := "https://www.googleapis.com/compute/v1/projects/a/zones/b/instances/c"
	res := fmtInstanceSelfLink("a", "b", "c")
	if res != expected {
		t.Errorf("Unexpected result from fmtInstanceSelfLink")
	}
}

type poolFuncTracker struct {
	called bool
}

func (p *poolFuncTracker) track(_, _ string) error {
	p.called = true
	return nil
}

func newPoolTracker() *poolFuncTracker {
	return &poolFuncTracker{
		called: false,
	}
}

func TestProcessTargetPools(t *testing.T) {
	_, mockComputeService := computeservice.NewComputeServiceMock()
	projectID := "testProject"
	instanceName := "testInstance"
	tpPresent := []string{
		"pool1",
	}
	tpEmpty := []string{}
	machineScope := machineScope{
		machine: &machinev1beta1.Machine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      instanceName,
				Namespace: "",
			},
		},
		coreClient: controllerfake.NewFakeClient(),
		providerSpec: &gcpv1beta1.GCPMachineProviderSpec{
			Zone: "zone1",
		},
		projectID:      projectID,
		providerStatus: &gcpv1beta1.GCPMachineProviderStatus{},
		computeService: mockComputeService,
	}
	tCases := []struct {
		expectedCall bool
		desired      bool
		region       string
		targetPools  []string
	}{
		{
			// Delete when present
			expectedCall: true,
			desired:      false,
			region:       computeservice.WithMachineInPool,
			targetPools:  tpPresent,
		},
		{
			// Create when absent
			expectedCall: true,
			desired:      true,
			region:       computeservice.NoMachinesInPool,
			targetPools:  tpPresent,
		},
		{
			// Delete when absent
			expectedCall: false,
			desired:      false,
			region:       computeservice.NoMachinesInPool,
			targetPools:  tpPresent,
		},
		{
			// Create when present
			expectedCall: false,
			desired:      true,
			region:       computeservice.WithMachineInPool,
			targetPools:  tpPresent,
		},
		{
			// Return early when TP is empty list
			expectedCall: false,
			desired:      true,
			region:       computeservice.WithMachineInPool,
			targetPools:  tpEmpty,
		},
		{
			// Return early when TP is nil
			expectedCall: false,
			desired:      true,
			region:       computeservice.WithMachineInPool,
			targetPools:  nil,
		},
	}
	for i, tc := range tCases {
		pt := newPoolTracker()
		machineScope.providerSpec.Region = tc.region
		machineScope.providerSpec.TargetPools = tc.targetPools
		rec := newReconciler(&machineScope)
		err := rec.processTargetPools(tc.desired, pt.track)
		if err != nil {
			t.Errorf("unexpected error from ptp")
		}
		if pt.called != tc.expectedCall {
			t.Errorf("tc %v: expected didn't match observed: %v, %v", i, tc.expectedCall, pt.called)
		}
	}
}
