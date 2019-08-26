package machine

import (
	"testing"

	machinev1 "github.com/openshift/cluster-api/pkg/apis/machine/v1beta1"
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
	// Initialize machine actuator.
	machineActuator := NewActuator(ActuatorParams{
		CoreClient:    fake.NewFakeClient(),
		EventRecorder: recorder,
	})
	if machineActuator == nil {
		t.Errorf("expected machine not nil")
	}
}
