package machine

import (
	"errors"
	"fmt"
	"testing"

	gcpv1beta1 "github.com/openshift/cluster-api-provider-gcp/pkg/apis/gcpprovider/v1beta1"
	computeservice "github.com/openshift/cluster-api-provider-gcp/pkg/cloud/gcp/actuators/services/compute"
	machinev1beta1 "github.com/openshift/machine-api-operator/pkg/apis/machine/v1beta1"
	machinecontroller "github.com/openshift/machine-api-operator/pkg/controller/machine"
	compute "google.golang.org/api/compute/v1"
	googleapi "google.golang.org/api/googleapi"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	controllerfake "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestCreate(t *testing.T) {
	cases := []struct {
		name                string
		labels              map[string]string
		providerSpec        *gcpv1beta1.GCPMachineProviderSpec
		expectedCondition   *gcpv1beta1.GCPMachineProviderCondition
		secret              *corev1.Secret
		mockInstancesInsert func(project string, zone string, instance *compute.Instance) (*compute.Operation, error)
		validateInstance    func(t *testing.T, instance *compute.Instance)
		expectedError       error
	}{
		{
			name: "Successfully create machine",
			expectedCondition: &gcpv1beta1.GCPMachineProviderCondition{
				Type:    gcpv1beta1.MachineCreated,
				Status:  corev1.ConditionTrue,
				Reason:  machineCreationSucceedReason,
				Message: machineCreationSucceedMessage,
			},
			expectedError: nil,
		},
		{
			name: "Fail on invalid target pools",
			providerSpec: &gcpv1beta1.GCPMachineProviderSpec{
				TargetPools: []string{""},
			},
			expectedError: errors.New("failed validating machine provider spec: all target pools must have valid name"),
		},
		{
			name: "Fail on invalid missing machine label",
			labels: map[string]string{
				machinev1beta1.MachineClusterIDLabel: "",
			},
			expectedError: errors.New("failed validating machine provider spec: machine is missing \"machine.openshift.io/cluster-api-cluster\" label"),
		},
		{
			name: "Fail on invalid user data secret",
			providerSpec: &gcpv1beta1.GCPMachineProviderSpec{
				UserDataSecret: &corev1.LocalObjectReference{
					Name: "notvalid",
				},
			},
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name: "notvalid",
				},
				Data: map[string][]byte{
					"badKey": []byte(""),
				},
			},
			expectedError: errors.New("error getting custom user data: secret /notvalid does not have \"userData\" field set. Thus, no user data applied when creating an instance"),
		},
		{
			name:          "Fail on compute service error",
			expectedError: errors.New("failed to create instance via compute service: fail"),
			expectedCondition: &gcpv1beta1.GCPMachineProviderCondition{
				Type:    gcpv1beta1.MachineCreated,
				Status:  corev1.ConditionFalse,
				Reason:  machineCreationFailedReason,
				Message: "fail",
			},
			mockInstancesInsert: func(project string, zone string, instance *compute.Instance) (*compute.Operation, error) {
				return nil, errors.New("fail")
			},
		},
		{
			name:          "Fail on google api error",
			expectedError: machinecontroller.InvalidMachineConfiguration("error launching instance: %v", "googleapi: Error 400: error"),
			expectedCondition: &gcpv1beta1.GCPMachineProviderCondition{
				Type:    gcpv1beta1.MachineCreated,
				Status:  corev1.ConditionFalse,
				Reason:  machineCreationFailedReason,
				Message: "googleapi: Error 400: error",
			},
			mockInstancesInsert: func(project string, zone string, instance *compute.Instance) (*compute.Operation, error) {
				return nil, &googleapi.Error{Message: "error", Code: 400}
			},
		},
		{
			name: "Use projectID from NetworkInterface if set",
			providerSpec: &gcpv1beta1.GCPMachineProviderSpec{
				ProjectID: "project",
				Region:    "test-region",
				NetworkInterfaces: []*gcpv1beta1.GCPNetworkInterface{
					{
						ProjectID:  "network-project",
						Network:    "test-network",
						Subnetwork: "test-subnetwork",
					},
				},
			},
			validateInstance: func(t *testing.T, instance *compute.Instance) {
				if len(instance.NetworkInterfaces) != 1 {
					t.Errorf("expected one network interface, got %d", len(instance.NetworkInterfaces))
				}
				expectedNetwork := fmt.Sprintf("projects/%s/global/networks/%s", "network-project", "test-network")
				if instance.NetworkInterfaces[0].Network != expectedNetwork {
					t.Errorf("Expected Network: %q, Got Network: %q", expectedNetwork, instance.NetworkInterfaces[0].Network)
				}
				expectedSubnetwork := fmt.Sprintf("projects/%s/regions/%s/networks/%s", "network-project", "test-region", "test-network")
				if instance.NetworkInterfaces[0].Network != expectedNetwork {
					t.Errorf("Expected Network: %q, Got Network: %q", expectedSubnetwork, instance.NetworkInterfaces[0].Subnetwork)
				}
			},
		},
		{
			name: "guestAccelerators are correctly passed to the api",
			providerSpec: &gcpv1beta1.GCPMachineProviderSpec{
				Region:      "test-region",
				Zone:        "test-zone",
				MachineType: "n1-test-machineType",
				GuestAccelerators: []*gcpv1beta1.GCPAcceleratorConfig{
					{
						AcceleratorType:  "nvidia-tesla-v100",
						AcceleratorCount: 2,
					},
				},
			},
			validateInstance: func(t *testing.T, instance *compute.Instance) {
				if len(instance.GuestAccelerators) != 1 {
					return // to avoid index out of range error
				}
				expectedAcceleratorType := fmt.Sprintf("zones/%s/acceleratorTypes/%s", "test-zone", "nvidia-tesla-v100")
				if instance.GuestAccelerators[0].AcceleratorType != expectedAcceleratorType {
					t.Errorf("Expected AcceleratorType: %q, Got: %q", expectedAcceleratorType, instance.GuestAccelerators[0].AcceleratorType)
				}
				var expectedAcceleratorCount int64 = 2
				if instance.GuestAccelerators[0].AcceleratorCount != expectedAcceleratorCount {
					t.Errorf("Expected AcceleratorCount: %d, Got: %d", expectedAcceleratorCount, instance.GuestAccelerators[0].AcceleratorCount)
				}
			},
		},
		{
			name: "Use projectID from ProviderSpec if not set in the NetworkInterface",
			providerSpec: &gcpv1beta1.GCPMachineProviderSpec{
				ProjectID: "project",
				Region:    "test-region",
				NetworkInterfaces: []*gcpv1beta1.GCPNetworkInterface{
					{
						Network:    "test-network",
						Subnetwork: "test-subnetwork",
					},
				},
			},
			validateInstance: func(t *testing.T, instance *compute.Instance) {
				if len(instance.NetworkInterfaces) != 1 {
					t.Errorf("expected one network interface, got %d", len(instance.NetworkInterfaces))
				}
				expectedNetwork := fmt.Sprintf("projects/%s/global/networks/%s", "project", "test-network")
				if instance.NetworkInterfaces[0].Network != expectedNetwork {
					t.Errorf("Expected Network: %q, Got Network: %q", expectedNetwork, instance.NetworkInterfaces[0].Network)
				}
				expectedSubnetwork := fmt.Sprintf("projects/%s/regions/%s/networks/%s", "project", "test-region", "test-network")
				if instance.NetworkInterfaces[0].Network != expectedNetwork {
					t.Errorf("Expected Network: %q, Got Network: %q", expectedSubnetwork, instance.NetworkInterfaces[0].Subnetwork)
				}
			},
		},
		{
			name: "Set disk encryption correctly when EncryptionKey is provided (with projectID)",
			providerSpec: &gcpv1beta1.GCPMachineProviderSpec{
				ProjectID: "project",
				Region:    "test-region",
				Disks: []*gcpv1beta1.GCPDisk{
					{
						EncryptionKey: &gcpv1beta1.GCPEncryptionKeyReference{
							KMSKey: &gcpv1beta1.GCPKMSKeyReference{
								Name:      "kms-key-name",
								KeyRing:   "kms-key-ring",
								ProjectID: "kms-project",
								Location:  "global",
							},
							KMSKeyServiceAccount: "kms-key-service-account",
						},
					},
				},
			},
			validateInstance: func(t *testing.T, instance *compute.Instance) {
				if len(instance.Disks) != 1 {
					t.Errorf("expected one disk, got %d", len(instance.Disks))
				}
				diskEncryption := instance.Disks[0].DiskEncryptionKey
				if diskEncryption == nil {
					t.Errorf("Expected DiskEncrpytionKey but got nil")
				}
				expectedKmsKeyName := "projects/kms-project/locations/global/keyRings/kms-key-ring/cryptoKeys/kms-key-name"
				if diskEncryption.KmsKeyName != expectedKmsKeyName {
					t.Errorf("Expected KmsKeyName: %q, Got KmsKeyName: %q", expectedKmsKeyName, diskEncryption.KmsKeyName)
				}
				expectedKmsKeyServiceAccount := "kms-key-service-account"
				if diskEncryption.KmsKeyServiceAccount != expectedKmsKeyServiceAccount {
					t.Errorf("Expected KmsKeyServiceAccount: %q, Got KmsKeyServiceAccount: %q", expectedKmsKeyServiceAccount, diskEncryption.KmsKeyServiceAccount)
				}
			},
		},
		{
			name: "Set disk encryption correctly when EncryptionKey is provided (without projectID)",
			providerSpec: &gcpv1beta1.GCPMachineProviderSpec{
				ProjectID: "project",
				Region:    "test-region",
				Disks: []*gcpv1beta1.GCPDisk{
					{
						EncryptionKey: &gcpv1beta1.GCPEncryptionKeyReference{
							KMSKey: &gcpv1beta1.GCPKMSKeyReference{
								Name:     "kms-key",
								KeyRing:  "kms-ring",
								Location: "centralus-1",
							},
						},
					},
				},
			},
			validateInstance: func(t *testing.T, instance *compute.Instance) {
				if len(instance.Disks) != 1 {
					t.Errorf("expected one disk, got %d", len(instance.Disks))
				}
				diskEncryption := instance.Disks[0].DiskEncryptionKey
				if diskEncryption == nil {
					t.Errorf("Expected DiskEncrpytionKey but got nil")
				}
				expectedKmsKeyName := "projects/project/locations/centralus-1/keyRings/kms-ring/cryptoKeys/kms-key"
				if diskEncryption.KmsKeyName != expectedKmsKeyName {
					t.Errorf("Expected KmsKeyName: %q, Got KmsKeyName: %q", expectedKmsKeyName, diskEncryption.KmsKeyName)
				}
				expectedKmsKeyServiceAccount := ""
				if diskEncryption.KmsKeyServiceAccount != expectedKmsKeyServiceAccount {
					t.Errorf("Expected KmsKeyServiceAccount: %q, Got KmsKeyServiceAccount: %q", expectedKmsKeyServiceAccount, diskEncryption.KmsKeyServiceAccount)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			receivedInstance, mockComputeService := computeservice.NewComputeServiceMock()
			providerSpec := &gcpv1beta1.GCPMachineProviderSpec{}
			labels := map[string]string{
				machinev1beta1.MachineClusterIDLabel: "CLUSTERID",
			}

			if tc.providerSpec != nil {
				providerSpec = tc.providerSpec
			}

			if tc.labels != nil {
				labels = tc.labels
			}

			machineScope := machineScope{
				machine: &machinev1beta1.Machine{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "",
						Namespace: "",
						Labels:    labels,
					},
				},
				coreClient:     controllerfake.NewFakeClient(),
				providerSpec:   providerSpec,
				providerStatus: &gcpv1beta1.GCPMachineProviderStatus{},
				computeService: mockComputeService,
				projectID:      providerSpec.ProjectID,
			}

			reconciler := newReconciler(&machineScope)

			if tc.secret != nil {
				reconciler.coreClient = controllerfake.NewFakeClientWithScheme(scheme.Scheme, tc.secret)
			}

			if tc.mockInstancesInsert != nil {
				mockComputeService.MockInstancesInsert = tc.mockInstancesInsert
			}

			err := reconciler.create()

			if tc.expectedCondition != nil {
				if reconciler.providerStatus.Conditions[0].Type != tc.expectedCondition.Type {
					t.Errorf("Expected: %s, got %s", tc.expectedCondition.Type, reconciler.providerStatus.Conditions[0].Type)
				}
				if reconciler.providerStatus.Conditions[0].Status != tc.expectedCondition.Status {
					t.Errorf("Expected: %s, got %s", tc.expectedCondition.Status, reconciler.providerStatus.Conditions[0].Status)
				}
				if reconciler.providerStatus.Conditions[0].Reason != tc.expectedCondition.Reason {
					t.Errorf("Expected: %s, got %s", tc.expectedCondition.Reason, reconciler.providerStatus.Conditions[0].Reason)
				}
				if reconciler.providerStatus.Conditions[0].Message != tc.expectedCondition.Message {
					t.Errorf("Expected: %s, got %s", tc.expectedCondition.Message, reconciler.providerStatus.Conditions[0].Message)
				}
			}

			if tc.expectedError != nil {
				if err == nil {
					t.Error("reconciler was expected to return error")
				}
				if err.Error() != tc.expectedError.Error() {
					t.Errorf("Expected: %v, got %v", tc.expectedError, err)
				}
			} else {
				if err != nil {
					t.Errorf("reconciler was not expected to return error: %v", err)
				}
			}

			if tc.validateInstance != nil {
				tc.validateInstance(t, receivedInstance)
			}
		})
	}
}

func TestReconcileMachineWithCloudState(t *testing.T) {
	_, mockComputeService := computeservice.NewComputeServiceMock()

	zone := "us-east1-b"
	projecID := "testProject"
	instanceName := "testInstance"
	machineScope := machineScope{
		machine: &machinev1beta1.Machine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      instanceName,
				Namespace: "",
			},
		},
		coreClient: controllerfake.NewFakeClient(),
		providerSpec: &gcpv1beta1.GCPMachineProviderSpec{
			Zone: zone,
		},
		projectID:      projecID,
		providerID:     fmt.Sprintf("gce://%s/%s/%s", projecID, zone, instanceName),
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
	if err := r.reconcileMachineWithCloudState(nil); err != nil {
		t.Errorf("reconciler was not expected to return error: %v", err)
	}
	if r.machine.Status.Addresses[0] != expectedNodeAddresses[0] {
		t.Errorf("Expected: %s, got: %s", expectedNodeAddresses[0], r.machine.Status.Addresses[0])
	}
	if r.machine.Status.Addresses[1] != expectedNodeAddresses[1] {
		t.Errorf("Expected: %s, got: %s", expectedNodeAddresses[1], r.machine.Status.Addresses[1])
	}

	if r.providerID != *r.machine.Spec.ProviderID {
		t.Errorf("Expected: %s, got: %s", r.providerID, *r.machine.Spec.ProviderID)
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
				Labels: map[string]string{
					machinev1beta1.MachineClusterIDLabel: "CLUSTERID",
				},
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
				Labels: map[string]string{
					machinev1beta1.MachineClusterIDLabel: "CLUSTERID",
				},
			},
		},
		coreClient:     controllerfake.NewFakeClient(),
		providerSpec:   &gcpv1beta1.GCPMachineProviderSpec{},
		providerStatus: &gcpv1beta1.GCPMachineProviderStatus{},
		computeService: mockComputeService,
	}
	reconciler := newReconciler(&machineScope)
	if err := reconciler.delete(); err != nil {
		if _, ok := err.(*machinecontroller.RequeueAfterError); !ok {
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
	projecID := "testProject"
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
		projectID:      projecID,
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

func TestGetUserData(t *testing.T) {
	userDataSecretName := "test"
	defaultNamespace := "test"
	userDataBlob := "test"
	machineScope := machineScope{
		machine: &machinev1beta1.Machine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "",
				Namespace: defaultNamespace,
			},
		},
		providerSpec: &gcpv1beta1.GCPMachineProviderSpec{
			UserDataSecret: &corev1.LocalObjectReference{
				Name: userDataSecretName,
			},
		},
		providerStatus: &gcpv1beta1.GCPMachineProviderStatus{},
	}
	reconciler := newReconciler(&machineScope)

	testCases := []struct {
		secret *corev1.Secret
		error  error
	}{
		{
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      userDataSecretName,
					Namespace: defaultNamespace,
				},
				Data: map[string][]byte{
					userDataSecretKey: []byte(userDataBlob),
				},
			},
			error: nil,
		},
		{
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "notFound",
					Namespace: defaultNamespace,
				},
				Data: map[string][]byte{
					userDataSecretKey: []byte(userDataBlob),
				},
			},
			error: &machinecontroller.MachineError{},
		},
		{
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      userDataSecretName,
					Namespace: defaultNamespace,
				},
				Data: map[string][]byte{
					"badKey": []byte(userDataBlob),
				},
			},
			error: &machinecontroller.MachineError{},
		},
	}

	for _, tc := range testCases {
		reconciler.coreClient = controllerfake.NewFakeClientWithScheme(scheme.Scheme, tc.secret)
		userData, err := reconciler.getCustomUserData()
		if tc.error != nil {
			if err == nil {
				t.Fatal("Expected error")
			}
			_, expectMachineError := tc.error.(*machinecontroller.MachineError)
			_, gotMachineError := err.(*machinecontroller.MachineError)
			if expectMachineError && !gotMachineError || !expectMachineError && gotMachineError {
				t.Errorf("Expected %T, got: %T", tc.error, err)
			}
		} else {
			if userData != userDataBlob {
				t.Errorf("Expected: %v, got: %v", userDataBlob, userData)
			}
		}
	}
}

func TestSetMachineCloudProviderSpecifics(t *testing.T) {
	testType := "testType"
	testRegion := "testRegion"
	testZone := "testZone"
	testStatus := "testStatus"

	r := Reconciler{
		machineScope: &machineScope{
			machine: &machinev1beta1.Machine{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "",
					Namespace: "",
				},
			},
			providerSpec: &gcpv1beta1.GCPMachineProviderSpec{
				MachineType: testType,
				Region:      testRegion,
				Zone:        testZone,
				Preemptible: true,
			},
		},
	}

	instance := &compute.Instance{
		Status: testStatus,
	}

	r.setMachineCloudProviderSpecifics(instance)

	actualInstanceStateAnnotation := r.machine.Annotations[machinecontroller.MachineInstanceStateAnnotationName]
	if actualInstanceStateAnnotation != instance.Status {
		t.Errorf("Expected instance state annotation: %v, got: %v", actualInstanceStateAnnotation, instance.Status)
	}

	actualMachineTypeLabel := r.machine.Labels[machinecontroller.MachineInstanceTypeLabelName]
	if actualMachineTypeLabel != r.providerSpec.MachineType {
		t.Errorf("Expected machine type label: %v, got: %v", actualMachineTypeLabel, r.providerSpec.MachineType)
	}

	actualMachineRegionLabel := r.machine.Labels[machinecontroller.MachineRegionLabelName]
	if actualMachineRegionLabel != r.providerSpec.Region {
		t.Errorf("Expected machine region label: %v, got: %v", actualMachineRegionLabel, r.providerSpec.Region)
	}

	actualMachineAZLabel := r.machine.Labels[machinecontroller.MachineAZLabelName]
	if actualMachineAZLabel != r.providerSpec.Zone {
		t.Errorf("Expected machine zone label: %v, got: %v", actualMachineAZLabel, r.providerSpec.Zone)
	}

	if _, ok := r.machine.Spec.Labels[machinecontroller.MachineInterruptibleInstanceLabelName]; !ok {
		t.Error("Missing spot instance label in machine spec")
	}
}
