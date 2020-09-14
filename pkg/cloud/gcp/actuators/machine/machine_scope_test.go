package machine

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	configv1 "github.com/openshift/api/config/v1"
	gcpv1 "github.com/openshift/cluster-api-provider-gcp/pkg/apis/gcpprovider/v1beta1"
	computeservice "github.com/openshift/cluster-api-provider-gcp/pkg/cloud/gcp/actuators/services/compute"
	machinev1 "github.com/openshift/machine-api-operator/pkg/apis/machine/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	controllerfake "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

func init() {
	configv1.AddToScheme(scheme.Scheme)
	machinev1.AddToScheme(scheme.Scheme)
}

func TestNewMachineScope(t *testing.T) {
	g := NewWithT(t)

	userDataSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      userDataSecretName,
			Namespace: defaultNamespaceName,
		},
		Data: map[string][]byte{
			userDataSecretKey: []byte("userDataBlob"),
		},
	}

	credentialsSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      credentialsSecretName,
			Namespace: defaultNamespaceName,
		},
		Data: map[string][]byte{
			credentialsSecretKey: []byte("{\"project_id\": \"test\"}"),
		},
	}

	invalidCredentialsSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      credentialsSecretName,
			Namespace: defaultNamespaceName,
		},
		Data: map[string][]byte{
			credentialsSecretKey: []byte("1"),
		},
	}

	fakeClient := controllerfake.NewFakeClient(userDataSecret, credentialsSecret)

	validProviderSpec, err := gcpv1.RawExtensionFromProviderSpec(&gcpv1.GCPMachineProviderSpec{
		CredentialsSecret: &corev1.LocalObjectReference{
			Name: credentialsSecretName,
		},
	})
	g.Expect(err).ToNot(HaveOccurred())

	cases := []struct {
		name          string
		params        machineScopeParams
		expectedError error
	}{
		{
			name: "successfully create machine scope",
			params: machineScopeParams{
				coreClient:           fakeClient,
				computeClientBuilder: computeservice.MockBuilderFuncType,
				machine: &machinev1.Machine{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test",
						Namespace: defaultNamespaceName,
						Labels: map[string]string{
							machinev1.MachineClusterIDLabel: "CLUSTERID",
						},
					},
					Spec: machinev1.MachineSpec{
						ProviderSpec: machinev1.ProviderSpec{
							Value: validProviderSpec,
						},
					}},
			},
		},
		{
			name: "fail to get provider spec",
			params: machineScopeParams{
				coreClient:           fakeClient,
				computeClientBuilder: computeservice.MockBuilderFuncType,
				machine: &machinev1.Machine{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test",
						Namespace: defaultNamespaceName,
						Labels: map[string]string{
							machinev1.MachineClusterIDLabel: "CLUSTERID",
						},
					},
					Spec: machinev1.MachineSpec{
						ProviderSpec: machinev1.ProviderSpec{
							Value: &runtime.RawExtension{
								Raw: []byte{'1'},
							},
						},
					}},
			},
			expectedError: errors.New("failed to get machine config: error unmarshalling providerSpec: error unmarshaling JSON: while decoding JSON: json: cannot unmarshal number into Go value of type v1beta1.GCPMachineProviderSpec"),
		},
		{
			name: "fail to get provider status",
			params: machineScopeParams{
				coreClient:           fakeClient,
				computeClientBuilder: computeservice.MockBuilderFuncType,
				machine: &machinev1.Machine{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test",
						Namespace: defaultNamespaceName,
						Labels: map[string]string{
							machinev1.MachineClusterIDLabel: "CLUSTERID",
						},
					},
					Spec: machinev1.MachineSpec{
						ProviderSpec: machinev1.ProviderSpec{
							Value: validProviderSpec,
						},
					},
					Status: machinev1.MachineStatus{
						ProviderStatus: &runtime.RawExtension{
							Raw: []byte{'1'},
						},
					},
				},
			},
			expectedError: errors.New("failed to get machine provider status: error unmarshalling providerStatus: error unmarshaling JSON: while decoding JSON: json: cannot unmarshal number into Go value of type v1beta1.GCPMachineProviderStatus"),
		},
		{
			name: "fail to get credentials secret",
			params: machineScopeParams{
				coreClient:           controllerfake.NewFakeClient(userDataSecret),
				computeClientBuilder: computeservice.MockBuilderFuncType,
				machine: &machinev1.Machine{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test",
						Namespace: defaultNamespaceName,
						Labels: map[string]string{
							machinev1.MachineClusterIDLabel: "CLUSTERID",
						},
					},
					Spec: machinev1.MachineSpec{
						ProviderSpec: machinev1.ProviderSpec{
							Value: validProviderSpec,
						},
					}},
			},
			expectedError: errors.New("error getting credentials secret \"credentials-test\" in namespace \"test\": secrets \"credentials-test\" not found"),
		},
		{
			name: "fail to get project from json key",
			params: machineScopeParams{
				coreClient:           controllerfake.NewFakeClient(userDataSecret, invalidCredentialsSecret),
				computeClientBuilder: computeservice.MockBuilderFuncType,
				machine: &machinev1.Machine{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test",
						Namespace: defaultNamespaceName,
						Labels: map[string]string{
							machinev1.MachineClusterIDLabel: "CLUSTERID",
						},
					},
					Spec: machinev1.MachineSpec{
						ProviderSpec: machinev1.ProviderSpec{
							Value: validProviderSpec,
						},
					}},
			},
			expectedError: errors.New(`error getting project from JSON key: error un marshalling JSON key: json: cannot unmarshal number into Go value of type struct { ProjectID string "json:\"project_id\"" }`),
		},
		{
			name: "fail to create compute service",
			params: machineScopeParams{
				coreClient: fakeClient,
				computeClientBuilder: func(serviceAccountJSON string) (computeservice.GCPComputeService, error) {
					return nil, errors.New("test error")
				},
				machine: &machinev1.Machine{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test",
						Namespace: defaultNamespaceName,
						Labels: map[string]string{
							machinev1.MachineClusterIDLabel: "CLUSTERID",
						},
					},
					Spec: machinev1.MachineSpec{
						ProviderSpec: machinev1.ProviderSpec{
							Value: validProviderSpec,
						},
					}},
			},
			expectedError: errors.New("error creating compute service: test error"),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gs := NewWithT(t)
			scope, err := newMachineScope(tc.params)

			if tc.expectedError != nil {
				gs.Expect(err).To(HaveOccurred())
				gs.Expect(err.Error()).To(Equal(tc.expectedError.Error()))
			} else {
				gs.Expect(err).ToNot(HaveOccurred())
				gs.Expect(scope.Context).To(Equal(context.Background()))
				gs.Expect(scope.providerID).To(Equal("gce://test//test"))
			}
		})
	}
}

func TestPatchMachine(t *testing.T) {
	g := NewWithT(t)
	ctx := context.Background()

	testEnv := &envtest.Environment{
		CRDDirectoryPaths: []string{filepath.Join("..", "..", "..", "..", "..", "config", "crds")},
	}

	cfg, err := testEnv.Start()
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(cfg).ToNot(BeNil())
	defer func() {
		g.Expect(testEnv.Stop()).To(Succeed())
	}()

	k8sClient, err := client.New(cfg, client.Options{})
	g.Expect(err).ToNot(HaveOccurred())

	testNamespaceName := "test"

	testNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: testNamespaceName,
		},
	}
	g.Expect(k8sClient.Create(ctx, testNamespace)).To(Succeed())
	defer func() {
		g.Expect(k8sClient.Delete(ctx, testNamespace)).To(Succeed())
	}()

	userDataSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      userDataSecretName,
			Namespace: defaultNamespaceName,
		},
		Data: map[string][]byte{
			userDataSecretKey: []byte("userDataBlob"),
		},
	}

	g.Expect(k8sClient.Create(ctx, userDataSecret)).To(Succeed())
	defer func() {
		g.Expect(k8sClient.Delete(ctx, userDataSecret)).To(Succeed())
	}()

	credentialsSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      credentialsSecretName,
			Namespace: defaultNamespaceName,
		},
		Data: map[string][]byte{
			credentialsSecretKey: []byte("{\"project_id\": \"test\"}"),
		},
	}

	g.Expect(k8sClient.Create(ctx, credentialsSecret)).To(Succeed())
	defer func() {
		g.Expect(k8sClient.Delete(ctx, credentialsSecret)).To(Succeed())
	}()

	failedPhase := "Failed"

	machineName := "test"
	machineKey := types.NamespacedName{Namespace: testNamespaceName, Name: machineName}

	testCases := []struct {
		name   string
		mutate func(*machinev1.Machine)
		expect func(*machinev1.Machine) error
	}{
		{
			name: "Test changing labels",
			mutate: func(m *machinev1.Machine) {
				m.Labels["testlabel"] = "test"
			},
			expect: func(m *machinev1.Machine) error {
				if m.Labels["testlabel"] != "test" {
					return fmt.Errorf("label \"testlabel\" %q not equal expected \"test\"", m.ObjectMeta.Labels["test"])
				}
				return nil
			},
		},
		{
			name: "Test setting phase",
			mutate: func(m *machinev1.Machine) {
				m.Status.Phase = &failedPhase
			},
			expect: func(m *machinev1.Machine) error {
				if m.Status.Phase != nil && *m.Status.Phase == failedPhase {
					return nil
				}
				return fmt.Errorf("phase is nil or not equal expected \"Failed\"")
			},
		},
		{
			name: "Test setting provider status",
			mutate: func(m *machinev1.Machine) {
				instanceID := "123"
				instanceState := "running"

				providerStatus, err := gcpv1.RawExtensionFromProviderStatus(&gcpv1.GCPMachineProviderStatus{
					InstanceID:    &instanceID,
					InstanceState: &instanceState,
				})
				if err != nil {
					// This error should never happen
					panic(err)
				}

				m.Status.ProviderStatus = providerStatus
			},
			expect: func(m *machinev1.Machine) error {
				providerStatus, err := gcpv1.ProviderStatusFromRawExtension(m.Status.ProviderStatus)
				if err != nil {
					return fmt.Errorf("unable to get provider status: %v", err)
				}

				if providerStatus.InstanceID == nil || *providerStatus.InstanceID != "123" {
					return fmt.Errorf("instanceID is nil or not equal expected \"123\"")
				}

				if providerStatus.InstanceState == nil || *providerStatus.InstanceState != "running" {
					return fmt.Errorf("instanceState is nil or not equal expected \"running\"")
				}

				return nil
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			gs := NewWithT(t)
			timeout := 10 * time.Second

			// original objects
			originalProviderSpec := gcpv1.GCPMachineProviderSpec{
				CredentialsSecret: &corev1.LocalObjectReference{
					Name: credentialsSecretName,
				},
				UserDataSecret: &corev1.LocalObjectReference{
					Name: userDataSecretName,
				},
			}

			rawProviderSpec, err := gcpv1.RawExtensionFromProviderSpec(&originalProviderSpec)
			gs.Expect(err).ToNot(HaveOccurred())

			machine := &machinev1.Machine{
				ObjectMeta: metav1.ObjectMeta{
					Name:      machineName,
					Namespace: testNamespaceName,
					Labels:    map[string]string{},
				},
				TypeMeta: metav1.TypeMeta{
					Kind:       "Machine",
					APIVersion: "machine.openshift.io/v1beta1",
				},
				Spec: machinev1.MachineSpec{
					ProviderSpec: machinev1.ProviderSpec{
						Value: rawProviderSpec,
					},
				},
			}

			// Create the machine
			gs.Expect(k8sClient.Create(ctx, machine)).To(Succeed())
			defer func() {
				gs.Expect(k8sClient.Delete(ctx, machine)).To(Succeed())
			}()

			// Ensure the machine has synced to the cache
			getMachine := func() error {
				return k8sClient.Get(ctx, machineKey, machine)
			}
			gs.Eventually(getMachine, timeout).Should(Succeed())

			machineScope, err := newMachineScope(machineScopeParams{
				coreClient:           k8sClient,
				machine:              machine,
				Context:              ctx,
				computeClientBuilder: computeservice.MockBuilderFuncType,
			})

			gs.Expect(err).ToNot(HaveOccurred())

			tc.mutate(machineScope.machine)

			// Patch the machine and check the expectation from the test case
			// use Close() instead of Patch(), because Close() sets provider status and spec
			gs.Expect(machineScope.Close()).To(Succeed())
			checkExpectation := func() error {
				if err := getMachine(); err != nil {
					return err
				}
				return tc.expect(machine)
			}
			gs.Eventually(checkExpectation, timeout).Should(Succeed())

			// Check that resource version doesn't change if we call patchMachine() again
			machineResourceVersion := machine.ResourceVersion

			gs.Expect(machineScope.PatchMachine()).To(Succeed())
			gs.Eventually(getMachine, timeout).Should(Succeed())
			gs.Consistently(machine.ResourceVersion).Should(Equal(machineResourceVersion))
		})
	}
}
