package machine

import (
	"testing"

	gcpv1beta1 "github.com/openshift/cluster-api-provider-gcp/pkg/apis/gcpprovider/v1beta1"
	computeservice "github.com/openshift/cluster-api-provider-gcp/pkg/cloud/gcp/actuators/services/compute"
	machinev1beta1 "github.com/openshift/cluster-api/pkg/apis/machine/v1beta1"
	controllerError "github.com/openshift/cluster-api/pkg/controller/error"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	controllerfake "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func testMachine() *machinev1beta1.Machine {
	return &machinev1beta1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "machine",
			Namespace: "test",
		},
	}
}

func testMachineProviderConfig() *gcpv1beta1.GCPMachineProviderSpec {
	return &gcpv1beta1.GCPMachineProviderSpec{
		Zone: "zone1",
	}
}

func TestCreate(t *testing.T) {
	_, mockComputeService := computeservice.NewComputeServiceMock()

	machine := testMachine()
	providerSpec := testMachineProviderConfig()

	scope := scope{
		projectID:      "projectID",
		computeService: mockComputeService,
	}

	coreClient := controllerfake.NewFakeClient(machine)
	reconciler := newReconciler(&scope, coreClient)
	instance, err := reconciler.create(machine, providerSpec)
	if instance == nil {
		t.Error("reconciler was not expected to return nil instance")
	}
	if err != nil {
		t.Errorf("reconciler was not expected to return error: %v", err)
	}
}

func TestExists(t *testing.T) {
	_, mockComputeService := computeservice.NewComputeServiceMock()

	machine := testMachine()
	providerSpec := testMachineProviderConfig()

	scope := scope{
		projectID:      "projectID",
		computeService: mockComputeService,
	}

	coreClient := controllerfake.NewFakeClient(machine)
	reconciler := newReconciler(&scope, coreClient)

	exists, err := reconciler.exists(machine, providerSpec)
	if err != nil || exists != true {
		t.Errorf("reconciler was not expected to return error: %v", err)
	}
}

func TestDelete(t *testing.T) {
	_, mockComputeService := computeservice.NewComputeServiceMock()
	machine := testMachine()
	providerSpec := testMachineProviderConfig()

	scope := scope{
		projectID:      "projectID",
		computeService: mockComputeService,
	}

	coreClient := controllerfake.NewFakeClient(machine)
	reconciler := newReconciler(&scope, coreClient)

	if err := reconciler.delete(machine, providerSpec); err != nil {
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

func (p *poolFuncTracker) track(_, _, _, _ string) error {
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
	projecID := "testProject"
	instanceName := "testInstance"
	tpPresent := []string{
		"pool1",
	}
	tpEmpty := []string{}

	machine := testMachine()
	machine.Name = instanceName
	providerSpec := testMachineProviderConfig()

	scope := scope{
		projectID:      projecID,
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
		coreClient := controllerfake.NewFakeClient(machine)
		pt := newPoolTracker()
		rec := newReconciler(&scope, coreClient)
		err := rec.processTargetPools(tc.desired, tc.targetPools, pt.track, tc.region, providerSpec.Zone, machine.Name)
		if err != nil {
			t.Errorf("unexpected error from ptp")
		}
		if pt.called != tc.expectedCall {
			t.Errorf("tc %v: expected didn't match observed: %v, %v", i, tc.expectedCall, pt.called)
		}
	}
}
