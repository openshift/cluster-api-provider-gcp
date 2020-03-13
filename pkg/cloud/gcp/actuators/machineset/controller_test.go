/*
Copyright The Kubernetes Authors.
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package machineset

import (
	"encoding/json"
	"fmt"
	"testing"

	. "github.com/onsi/gomega"
	machineproviderv1 "github.com/openshift/cluster-api-provider-gcp/pkg/apis/gcpprovider/v1beta1"
	providerconfigv1 "github.com/openshift/cluster-api-provider-gcp/pkg/apis/gcpprovider/v1beta1"
	computeservice "github.com/openshift/cluster-api-provider-gcp/pkg/cloud/gcp/actuators/services/compute"
	machinev1 "github.com/openshift/machine-api-operator/pkg/apis/machine/v1beta1"
	"google.golang.org/api/compute/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func TestReconcile(t *testing.T) {
	mockMachineTypesFunc := func(_ string, _ string, machineType string) (*compute.MachineType, error) {
		switch machineType {
		case "n1-standard-2":
			return &compute.MachineType{
				GuestCpus: 2,
				MemoryMb:  7680,
			}, nil
		case "n2-highcpu-16":
			return &compute.MachineType{
				GuestCpus: 16,
				MemoryMb:  16384,
			}, nil
		default:
			return nil, fmt.Errorf("unknown machineType: %s", machineType)
		}
	}

	testCases := []struct {
		name                string
		machineType         string
		mockMachineTypesGet func(project string, zone string, machineType string) (*compute.MachineType, error)
		existingAnnotations map[string]string
		expectedAnnotations map[string]string
		expectErr           bool
	}{
		{
			name:        "with no machineType set",
			machineType: "",
			mockMachineTypesGet: func(_ string, _ string, _ string) (*compute.MachineType, error) {
				return nil, fmt.Errorf("machineType should not be empty")
			},
			existingAnnotations: make(map[string]string),
			expectedAnnotations: make(map[string]string),
			expectErr:           true,
		},
		{
			name:                "with a n1-standard-2",
			machineType:         "n1-standard-2",
			mockMachineTypesGet: mockMachineTypesFunc,
			existingAnnotations: make(map[string]string),
			expectedAnnotations: map[string]string{
				cpuKey:    "2",
				memoryKey: "7680",
			},
			expectErr: false,
		},
		{
			name:                "with a n2-highcpu-16",
			machineType:         "n2-highcpu-16",
			mockMachineTypesGet: mockMachineTypesFunc,
			existingAnnotations: make(map[string]string),
			expectedAnnotations: map[string]string{
				cpuKey:    "16",
				memoryKey: "16384",
			},
			expectErr: false,
		},
		{
			name:                "with existing annotations",
			machineType:         "n1-standard-2",
			mockMachineTypesGet: mockMachineTypesFunc,
			existingAnnotations: map[string]string{
				"existing": "annotation",
				"annother": "existingAnnotation",
			},
			expectedAnnotations: map[string]string{
				"existing": "annotation",
				"annother": "existingAnnotation",
				cpuKey:     "2",
				memoryKey:  "7680",
			},
			expectErr: false,
		},
		{
			name:                "with an invalid machineType",
			machineType:         "r4.xLarge",
			mockMachineTypesGet: mockMachineTypesFunc,
			existingAnnotations: map[string]string{
				"existing": "annotation",
				"annother": "existingAnnotation",
			},
			expectedAnnotations: map[string]string{
				"existing": "annotation",
				"annother": "existingAnnotation",
			},
			expectErr: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(tt *testing.T) {
			g := NewWithT(tt)

			_, service := computeservice.NewComputeServiceMock()
			if tc.mockMachineTypesGet != nil {
				service.MockMachineTypesGet = tc.mockMachineTypesGet
			}

			r := &Reconciler{
				cache: newMachineTypesCache(),
				getGCPService: func(_ string, _ providerconfigv1.GCPMachineProviderSpec) (computeservice.GCPComputeService, error) {
					return service, nil
				},
			}

			machineSet, err := newTestMachineSet("default", tc.machineType, tc.existingAnnotations)
			g.Expect(err).ToNot(HaveOccurred())

			_, err = r.reconcile(machineSet)
			g.Expect(err != nil).To(Equal(tc.expectErr))
			g.Expect(machineSet.Annotations).To(Equal(tc.expectedAnnotations))
		})
	}
}

func newTestMachineSet(namespace string, machineType string, existingAnnotations map[string]string) (*machinev1.MachineSet, error) {
	// Copy anntotations map so we don't modify the input
	annotations := make(map[string]string)
	for k, v := range existingAnnotations {
		annotations[k] = v
	}

	machineProviderSpec := &machineproviderv1.GCPMachineProviderSpec{
		MachineType: machineType,
	}
	providerSpec, err := providerSpecFromMachine(machineProviderSpec)
	if err != nil {
		return nil, err
	}

	return &machinev1.MachineSet{
		ObjectMeta: metav1.ObjectMeta{
			Annotations:  annotations,
			GenerateName: "test-machineset-",
			Namespace:    namespace,
		},
		Spec: machinev1.MachineSetSpec{
			Template: machinev1.MachineTemplateSpec{
				Spec: machinev1.MachineSpec{
					ProviderSpec: providerSpec,
				},
			},
		},
	}, nil
}

func providerSpecFromMachine(in *machineproviderv1.GCPMachineProviderSpec) (machinev1.ProviderSpec, error) {
	bytes, err := json.Marshal(in)
	if err != nil {
		return machinev1.ProviderSpec{}, err
	}
	return machinev1.ProviderSpec{
		Value: &runtime.RawExtension{Raw: bytes},
	}, nil
}
