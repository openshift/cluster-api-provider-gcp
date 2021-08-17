package machine

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	"github.com/openshift/cluster-api-provider-gcp/pkg/apis/gcpprovider/v1beta1"
	computeservice "github.com/openshift/cluster-api-provider-gcp/pkg/cloud/gcp/actuators/services/compute"
	machinev1 "github.com/openshift/machine-api-operator/pkg/apis/machine/v1beta1"

	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	controllerfake "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

var (
	userDataSecretName    = "user-data-test"
	credentialsSecretName = "credentials-test"
	defaultNamespaceName  = "test"
	credentialsSecretKey  = "service_account.json"
)

func init() {
	// Add types to scheme
	machinev1.AddToScheme(scheme.Scheme)
}

func TestActuatorEvents(t *testing.T) {
	g := NewWithT(t)
	timeout := 10 * time.Second

	testEnv := &envtest.Environment{
		CRDDirectoryPaths: []string{filepath.Join("..", "..", "..", "..", "..", "config", "crds")},
	}

	cfg, err := testEnv.Start()
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(cfg).ToNot(BeNil())
	defer func() {
		g.Expect(testEnv.Stop()).To(Succeed())
	}()

	mgr, err := manager.New(cfg, manager.Options{
		Scheme:             scheme.Scheme,
		MetricsBindAddress: "0",
	})
	if err != nil {
		t.Fatal(err)
	}

	mgrCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		g.Expect(mgr.Start(mgrCtx)).To(Succeed())
	}()

	k8sClient := mgr.GetClient()
	eventRecorder := mgr.GetEventRecorderFor("vspherecontroller")

	defaultNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: defaultNamespaceName,
		},
	}
	g.Expect(k8sClient.Create(context.Background(), defaultNamespace)).To(Succeed())
	defer func() {
		g.Expect(k8sClient.Delete(context.Background(), defaultNamespace)).To(Succeed())
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

	g.Expect(k8sClient.Create(context.Background(), userDataSecret)).To(Succeed())
	defer func() {
		g.Expect(k8sClient.Delete(context.Background(), userDataSecret)).To(Succeed())
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

	g.Expect(k8sClient.Create(context.Background(), credentialsSecret)).To(Succeed())
	defer func() {
		g.Expect(k8sClient.Delete(context.Background(), credentialsSecret)).To(Succeed())
	}()

	providerSpec, err := v1beta1.RawExtensionFromProviderSpec(&v1beta1.GCPMachineProviderSpec{
		CredentialsSecret: &corev1.LocalObjectReference{
			Name: credentialsSecretName,
		},
	})
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(providerSpec).ToNot(BeNil())

	cases := []struct {
		name      string
		error     string
		operation func(actuator *Actuator, machine *machinev1.Machine)
		event     string
	}{
		{
			name: "Create machine event failed on invalid machine scope",
			operation: func(actuator *Actuator, machine *machinev1.Machine) {
				machine.Spec = machinev1.MachineSpec{
					ProviderSpec: machinev1.ProviderSpec{
						Value: &runtime.RawExtension{
							Raw: []byte{'1'},
						},
					},
				}
				actuator.Create(context.Background(), machine)
			},
			event: "test: failed to create scope for machine: failed to get machine config: error unmarshalling providerSpec: error unmarshaling JSON: while decoding JSON: json: cannot unmarshal number into Go value of type v1beta1.GCPMachineProviderSpec",
		},
		{
			name: "Create machine event failed, reconciler's create failed",
			operation: func(actuator *Actuator, machine *machinev1.Machine) {
				machine.Labels[machinev1.MachineClusterIDLabel] = ""
				actuator.Create(context.Background(), machine)
			},
			event: "test: reconciler failed to Create machine: failed validating machine provider spec: machine is missing \"machine.openshift.io/cluster-api-cluster\" label",
		},
		{
			name: "Create machine event succeed",
			operation: func(actuator *Actuator, machine *machinev1.Machine) {
				actuator.Create(context.Background(), machine)
			},
			event: "Created Machine test",
		},
		{
			name: "Update machine event failed on invalid machine scope",
			operation: func(actuator *Actuator, machine *machinev1.Machine) {
				machine.Spec = machinev1.MachineSpec{
					ProviderSpec: machinev1.ProviderSpec{
						Value: &runtime.RawExtension{
							Raw: []byte{'1'},
						},
					},
				}
				actuator.Update(context.Background(), machine)
			},
			event: "test: failed to create scope for machine: failed to get machine config: error unmarshalling providerSpec: error unmarshaling JSON: while decoding JSON: json: cannot unmarshal number into Go value of type v1beta1.GCPMachineProviderSpec",
		},
		{
			name: "Update machine event failed, reconciler's update failed",
			operation: func(actuator *Actuator, machine *machinev1.Machine) {
				machine.Labels[machinev1.MachineClusterIDLabel] = ""
				actuator.Update(context.Background(), machine)
			},
			event: "test: reconciler failed to Update machine: failed validating machine provider spec: machine is missing \"machine.openshift.io/cluster-api-cluster\" label",
		},
		{
			name: "Update machine event succeed and only one event is created",
			operation: func(actuator *Actuator, machine *machinev1.Machine) {
				actuator.Update(context.Background(), machine)
				actuator.Update(context.Background(), machine)
			},
			event: "Updated Machine test",
		},
		{
			name: "Delete machine event failed on invalid machine scope",
			operation: func(actuator *Actuator, machine *machinev1.Machine) {
				machine.Spec = machinev1.MachineSpec{
					ProviderSpec: machinev1.ProviderSpec{
						Value: &runtime.RawExtension{
							Raw: []byte{'1'},
						},
					},
				}
				actuator.Delete(context.Background(), machine)
			},
			event: "test: failed to create scope for machine: failed to get machine config: error unmarshalling providerSpec: error unmarshaling JSON: while decoding JSON: json: cannot unmarshal number into Go value of type v1beta1.GCPMachineProviderSpec",
		},
		{
			name: "Delete machine event failed, reconciler's delete failed",
			operation: func(actuator *Actuator, machine *machinev1.Machine) {
				actuator.Delete(context.Background(), machine)
			},
			event: "test: reconciler failed to Delete machine: requeue in: 20s",
		},
		{
			name: "Delete machine event succeed",
			operation: func(actuator *Actuator, machine *machinev1.Machine) {
				actuator.computeClientBuilder = computeservice.MockBuilderFuncTypeNotFound
				actuator.Delete(context.Background(), machine)
			},
			event: "Deleted machine test",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gs := NewWithT(t)

			machine := &machinev1.Machine{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: defaultNamespaceName,
					Labels: map[string]string{
						machinev1.MachineClusterIDLabel: "CLUSTERID",
					},
				},
				Spec: machinev1.MachineSpec{
					ProviderSpec: machinev1.ProviderSpec{
						Value: providerSpec,
					},
				}}

			// Create the machine
			gs.Expect(k8sClient.Create(context.Background(), machine)).To(Succeed())

			// Make sure the machine and its event are deleted when the test ends
			defer func() {
				gs.Expect(k8sClient.Delete(context.Background(), machine)).To(Succeed())

				eventList := &v1.EventList{}
				gs.Expect(k8sClient.List(context.Background(), eventList, client.InNamespace(machine.Namespace))).To(Succeed())
				for i := range eventList.Items {
					gs.Expect(k8sClient.Delete(context.Background(), &eventList.Items[i])).To(Succeed())
				}
			}()

			// Ensure the machine has synced to the cache
			getMachine := func() error {
				machineKey := types.NamespacedName{Namespace: machine.Namespace, Name: machine.Name}
				return k8sClient.Get(context.Background(), machineKey, machine)
			}
			gs.Eventually(getMachine, timeout).Should(Succeed())

			params := ActuatorParams{
				CoreClient:           k8sClient,
				EventRecorder:        eventRecorder,
				ComputeClientBuilder: computeservice.MockBuilderFuncType,
			}

			actuator := NewActuator(params)
			tc.operation(actuator, machine)

			eventList := &v1.EventList{}
			waitForEvent := func() error {
				err := k8sClient.List(context.Background(), eventList, client.InNamespace(machine.Namespace))
				if err != nil {
					return err
				}

				if len(eventList.Items) != 1 {
					return fmt.Errorf("expected len 1, got %d", len(eventList.Items))
				}
				return nil
			}

			gs.Eventually(waitForEvent, timeout).Should(Succeed())

			gs.Expect(eventList.Items[0].Message).To(Equal(tc.event))
			gs.Expect(eventList.Items[0].Count).To(Equal(int32(1)))
		})
	}
}

func TestActuatorExists(t *testing.T) {
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

	providerSpec, err := v1beta1.RawExtensionFromProviderSpec(&v1beta1.GCPMachineProviderSpec{
		CredentialsSecret: &corev1.LocalObjectReference{
			Name: credentialsSecretName,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name        string
		expectError bool
	}{
		{
			name: "succefuly call reconciler exists",
		},
		{
			name:        "fail to call reconciler exists",
			expectError: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			machine := &machinev1.Machine{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: defaultNamespaceName,
					Labels: map[string]string{
						machinev1.MachineClusterIDLabel: "CLUSTERID",
					},
				},
				Spec: machinev1.MachineSpec{
					ProviderSpec: machinev1.ProviderSpec{
						Value: providerSpec,
					},
				}}

			if tc.expectError {
				machine.Spec = machinev1.MachineSpec{
					ProviderSpec: machinev1.ProviderSpec{
						Value: &runtime.RawExtension{
							Raw: []byte{'1'},
						},
					},
				}
			}

			params := ActuatorParams{
				CoreClient:           controllerfake.NewFakeClient(userDataSecret, credentialsSecret),
				ComputeClientBuilder: computeservice.MockBuilderFuncType,
			}

			actuator := NewActuator(params)

			_, err := actuator.Exists(nil, machine)

			if tc.expectError {
				if err == nil {
					t.Fatal("actuator exists expected to return an error")
				}
			} else {
				if err != nil {
					t.Fatal("actuator exists is not expected to return an error")
				}
			}
		})
	}

}
