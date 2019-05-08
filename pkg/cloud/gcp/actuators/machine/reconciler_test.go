package machine

import (
	"testing"

	gcpv1beta1 "github.com/openshift/cluster-api-provider-gcp/pkg/apis/gcpprovider/v1beta1"
	computeservice "github.com/openshift/cluster-api-provider-gcp/pkg/cloud/gcp/actuators/services/compute"
	"github.com/openshift/cluster-api/pkg/apis/machine/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	controllerfake "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestCreate(t *testing.T) {
	_, mockComputeService := computeservice.NewComputeServiceMock()
	machineScope := machineScope{
		machine: &v1beta1.Machine{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "",
				Namespace: "",
			},
		},
		coreClient:     controllerfake.NewFakeClient(),
		providerSpec:   &gcpv1beta1.GCPMachineProviderSpec{},
		computeService: mockComputeService,
	}
	reconciler := newReconciler(&machineScope)
	if err := reconciler.create(); err != nil {
		t.Errorf("reconciler was not expected to return error: %v", err)
	}
}
