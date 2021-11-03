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
	"context"
	"fmt"
	"strconv"

	"github.com/go-logr/logr"
	providerconfigv1 "github.com/openshift/cluster-api-provider-gcp/pkg/apis/gcpprovider/v1beta1"
	computeservice "github.com/openshift/cluster-api-provider-gcp/pkg/cloud/gcp/actuators/services/compute"
	"github.com/openshift/cluster-api-provider-gcp/pkg/cloud/gcp/actuators/util"
	machinev1 "github.com/openshift/machine-api-operator/pkg/apis/machine/v1beta1"
	mapierrors "github.com/openshift/machine-api-operator/pkg/controller/machine"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
)

const (
	// This exposes compute information based on the providerSpec input.
	// This is needed by the autoscaler to foresee upcoming capacity when scaling from zero.
	// https://github.com/openshift/enhancements/pull/186
	cpuKey    = "machine.openshift.io/vCPU"
	memoryKey = "machine.openshift.io/memoryMb"
	gpuKey    = "machine.openshift.io/GPU"
)

// Reconciler reconciles machineSets.
type Reconciler struct {
	Client client.Client
	Log    logr.Logger

	recorder record.EventRecorder
	scheme   *runtime.Scheme
	cache    *machineTypesCache

	// Allow a mock GCPComputeService to be injected during testing
	getGCPService func(namespace string, providerConfig providerconfigv1.GCPMachineProviderSpec) (computeservice.GCPComputeService, error)
}

// SetupWithManager creates a new controller for a manager.
func (r *Reconciler) SetupWithManager(mgr ctrl.Manager, options controller.Options) error {
	_, err := ctrl.NewControllerManagedBy(mgr).
		For(&machinev1.MachineSet{}).
		WithOptions(options).
		Build(r)

	if err != nil {
		return fmt.Errorf("failed setting up with a controller manager: %w", err)
	}

	r.cache = newMachineTypesCache()
	r.recorder = mgr.GetEventRecorderFor("machineset-controller")
	r.scheme = mgr.GetScheme()

	if r.getGCPService == nil {
		r.getGCPService = r.getRealGCPService
	}
	return nil
}

// Reconcile implements controller runtime Reconciler interface.
func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := r.Log.WithValues("machineset", req.Name, "namespace", req.Namespace)
	logger.V(3).Info("Reconciling")

	machineSet := &machinev1.MachineSet{}
	if err := r.Client.Get(ctx, req.NamespacedName, machineSet); err != nil {
		if apierrors.IsNotFound(err) {
			// Object not found, return. Created objects are automatically garbage collected.
			// For additional cleanup logic use finalizers.
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return ctrl.Result{}, err
	}

	// Ignore deleted MachineSets, this can happen when foregroundDeletion
	// is enabled
	if !machineSet.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}
	originalMachineSetToPatch := client.MergeFrom(machineSet.DeepCopy())

	result, err := r.reconcile(machineSet)
	if err != nil {
		logger.Error(err, "Failed to reconcile MachineSet")
		r.recorder.Eventf(machineSet, corev1.EventTypeWarning, "ReconcileError", "%v", err)
		// we don't return here so we want to attempt to patch the machine regardless of an error.
	}

	if err := r.Client.Patch(ctx, machineSet, originalMachineSetToPatch); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to patch machineSet: %v", err)
	}

	if isInvalidConfigurationError(err) {
		// For situations where requeuing won't help we don't return error.
		// https://github.com/kubernetes-sigs/controller-runtime/issues/617
		return result, nil
	}

	return result, err
}

func isInvalidConfigurationError(err error) bool {
	switch t := err.(type) {
	case *mapierrors.MachineError:
		if t.Reason == machinev1.InvalidConfigurationMachineError {
			return true
		}
	}
	return false
}

func (r *Reconciler) reconcile(machineSet *machinev1.MachineSet) (ctrl.Result, error) {
	providerConfig, err := getproviderConfig(machineSet)
	if err != nil {
		return ctrl.Result{}, mapierrors.InvalidMachineConfiguration("failed to get providerConfig: %v", err)
	}

	gceService, err := r.getGCPService(machineSet.GetNamespace(), *providerConfig)
	if err != nil {
		return ctrl.Result{}, err
	}

	machineType, err := r.cache.getMachineTypeFromCache(gceService, providerConfig.ProjectID, providerConfig.Zone, providerConfig.MachineType)
	if err != nil {
		return ctrl.Result{}, mapierrors.InvalidMachineConfiguration("error fetching machine type %q: %v", providerConfig.MachineType, err)
	} else if machineType == nil {
		// Returning no error to prevent further reconciliation, as user intervention is now required but emit an informational event
		r.recorder.Eventf(machineSet, corev1.EventTypeWarning, "FailedUpdate", "Failed to set autoscaling from zero annotations, machine type unknown")
		return ctrl.Result{}, nil
	}

	if machineSet.Annotations == nil {
		machineSet.Annotations = make(map[string]string)
	}

	// TODO: get annotations keys from machine API
	machineSet.Annotations[cpuKey] = strconv.FormatInt(machineType.GuestCpus, 10)
	machineSet.Annotations[memoryKey] = strconv.FormatInt(machineType.MemoryMb, 10)

	switch {
	case len(providerConfig.GuestAccelerators) > 0:
		// Guest accelerators will always be max size of 1
		machineSet.Annotations[gpuKey] = strconv.FormatInt(providerConfig.GuestAccelerators[0].AcceleratorCount, 10)
	case len(machineType.Accelerators) > 0:
		// Accelerators will always be max size of 1
		machineSet.Annotations[gpuKey] = strconv.FormatInt(machineType.Accelerators[0].GuestAcceleratorCount, 10)
	default:
		machineSet.Annotations[gpuKey] = strconv.FormatInt(0, 10)
	}

	return ctrl.Result{}, nil
}

func getproviderConfig(machineSet *machinev1.MachineSet) (*providerconfigv1.GCPMachineProviderSpec, error) {
	return providerconfigv1.ProviderSpecFromRawExtension(machineSet.Spec.Template.Spec.ProviderSpec.Value)
}

// getRealGCPService constructs a real GCPService for talking to GCP
func (r *Reconciler) getRealGCPService(namespace string, providerConfig providerconfigv1.GCPMachineProviderSpec) (computeservice.GCPComputeService, error) {
	serviceAccountJSON, err := util.GetCredentialsSecret(r.Client, namespace, providerConfig)
	if err != nil {
		return nil, err
	}

	computeService, err := computeservice.NewComputeService(serviceAccountJSON)
	if err != nil {
		return nil, mapierrors.InvalidMachineConfiguration("error creating compute service: %v", err)
	}
	return computeService, nil
}
