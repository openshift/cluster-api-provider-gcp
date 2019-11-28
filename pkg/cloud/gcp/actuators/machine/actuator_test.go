package machine

import (
	"testing"

	machinev1 "github.com/openshift/machine-api-operator/pkg/apis/machine/v1beta1"
	capifake "github.com/openshift/machine-api-operator/pkg/generated/clientset/versioned/fake"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func init() {
	// Add types to scheme
	machinev1.AddToScheme(scheme.Scheme)
}

func TestActuatorCreate(t *testing.T) {
	eventsChannel := make(chan string, 1)
	recorder := &record.FakeRecorder{
		Events: eventsChannel,
	}
	cs := capifake.NewSimpleClientset()
	// Initialize machine actuator.
	machineActuator := NewActuator(ActuatorParams{
		MachineClient: cs.MachineV1beta1(),
		CoreClient:    fake.NewFakeClient(),
		EventRecorder: recorder,
	})
	if machineActuator == nil {
		t.Errorf("expected machine not nil")
	}
}
