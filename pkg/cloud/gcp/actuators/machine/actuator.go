package machine

// This is a thin layer to implement the machine actuator interface with cloud provider details.
// The lifetime of scope and reconciler is a machine actuator operation.
// when scope is closed, it will persist to etcd the given machine spec and machine status (if modified)
import (
	"context"
	"fmt"
	"time"

	compute "google.golang.org/api/compute/v1"

	providerconfig "github.com/openshift/cluster-api-provider-gcp/pkg/apis/gcpprovider/v1beta1"
	clusterv1 "github.com/openshift/cluster-api/pkg/apis/cluster/v1alpha1"
	machinev1 "github.com/openshift/cluster-api/pkg/apis/machine/v1beta1"
	clustererror "github.com/openshift/cluster-api/pkg/controller/error"
	machinecontroller "github.com/openshift/cluster-api/pkg/controller/machine"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog"
	controllerclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	scopeFailFmt         = "%s: failed to create scope for machine: %v"
	createEventAction    = "Create"
	updateEventAction    = "Update"
	deleteEventAction    = "Delete"
	noEventAction        = ""
	credentialsSecretKey = "service_account.json"
)

// Actuator is responsible for performing machine reconciliation.
type Actuator struct {
	coreClient    controllerclient.Client
	eventRecorder record.EventRecorder
	codec         *providerconfig.GCPProviderConfigCodec
}

// ActuatorParams holds parameter information for Actuator.
type ActuatorParams struct {
	CoreClient    controllerclient.Client
	EventRecorder record.EventRecorder
	Codec         *providerconfig.GCPProviderConfigCodec
}

// NewActuator returns an actuator.
func NewActuator(params ActuatorParams) *Actuator {
	return &Actuator{
		coreClient:    params.CoreClient,
		eventRecorder: params.EventRecorder,
		codec:         params.Codec,
	}
}

// Set corresponding event based on error. It also returns the original error
// for convenience, so callers can do "return handleMachineError(...)".
func (a *Actuator) handleMachineError(machine *machinev1.Machine, err error, eventAction string) error {
	if eventAction != noEventAction {
		a.eventRecorder.Eventf(machine, corev1.EventTypeWarning, "Failed"+eventAction, "%v", err)
	}

	return err
}

// This expects the https://github.com/openshift/cloud-credential-operator to make a secret
// with a serviceAccount JSON Key content available. E.g:
//
//apiVersion: v1
//kind: Secret
//metadata:
//  name: gcp-sa
//  namespace: openshift-machine-api
//type: Opaque
//data:
//  serviceAccountJSON: base64 encoded content of the file
func (a *Actuator) getCredentialsSecret(machine *machinev1.Machine, spec *providerconfig.GCPMachineProviderSpec) (*corev1.Secret, error) {
	credentialsSecret := &corev1.Secret{}

	if err := a.coreClient.Get(context.Background(), controllerclient.ObjectKey{Namespace: machine.GetNamespace(), Name: spec.CredentialsSecret.Name}, credentialsSecret); err != nil {
		return nil, fmt.Errorf("error getting credentials secret %q in namespace %q: %v", spec.CredentialsSecret.Name, machine.GetNamespace(), err)
	}
	_, exists := credentialsSecret.Data[credentialsSecretKey]
	if !exists {
		return nil, fmt.Errorf("secret %v/%v does not have %q field set. Thus, no credentials applied when creating an instance", machine.GetNamespace(), spec.CredentialsSecret.Name, credentialsSecretKey)
	}

	return credentialsSecret, nil
}

// Create creates a machine and is invoked by the machine controller.
func (a *Actuator) Create(ctx context.Context, cluster *clusterv1.Cluster, machine *machinev1.Machine) error {
	klog.Infof("%s: Creating machine", machine.Name)

	providerSpec, err := providerConfigFromMachine(machine, a.codec)
	if err != nil {
		a.eventRecorder.Eventf(machine, corev1.EventTypeNormal, createEventAction, "Invalid machine %q configuration: %v", machine.Name, err)
		return fmt.Errorf("unable to decode machine provider config: %v", err)
	}

	credentialsSecret, err := a.getCredentialsSecret(machine, providerSpec)
	if err != nil {
		return fmt.Errorf("unable to get credentials secret: %v", err)
	}

	scope, err := newMachineScope(machineScopeParams{
		credentialsSecret: credentialsSecret,
		projectID:         providerSpec.ProjectID,
	})
	if err != nil {
		fmtErr := fmt.Sprintf(scopeFailFmt, machine.Name, err)
		return a.handleMachineError(machine, fmt.Errorf(fmtErr), createEventAction)
	}

	reconciler := newReconciler(scope, a.coreClient)
	instance, err := reconciler.create(machine, providerSpec)

	if instance != nil {
		modMachine, err := a.setMachineCloudProviderSpecifics(machine, instance)
		if err != nil {
			klog.Errorf("%s: error updating machine cloud provider specifics: %v", machine.Name, err)
		} else {
			machine = modMachine
		}

		modeMachine, err := a.updateStatus(ctx, machine, reconciler, instance)
		if err != nil {
			klog.Errorf("%s: error updating machine status: %v", machine.Name, err)
		} else {
			machine = modeMachine
		}

		modeMachine, err = a.updateProviderID(machine, scope)
		if err != nil {
			klog.Errorf("%s: error updating machine provider ID: %v", machine.Name, err)
		} else {
			machine = modeMachine
		}

		if instance.Status != "RUNNING" {
			errMsg := fmt.Errorf("%s: machine status is %q, requeuing...", machine.Name, instance.Status)
			klog.Info(errMsg)

			_, updateConditionError := a.updateMachineProviderConditions(machine, providerconfig.MachineCreated, machineCreationFailedReason, errMsg.Error(), corev1.ConditionFalse)
			if updateConditionError != nil {
				klog.Errorf("%s: error updating machine conditions: %v", machine.Name, updateConditionError)
			}

			return &clustererror.RequeueAfterError{RequeueAfter: requeueAfterSeconds * time.Second}
		}
	}

	if err != nil {
		modMachine, updateConditionError := a.updateMachineProviderConditions(machine, providerconfig.MachineCreated, machineCreationFailedReason, err.Error(), corev1.ConditionFalse)
		if updateConditionError != nil {
			klog.Errorf("%s: error updating machine conditions: %v", machine.Name, updateConditionError)
		} else {
			machine = modMachine
		}
		return a.handleMachineError(machine, err, createEventAction)
	}

	modMachine, updateConditionError := a.updateMachineProviderConditions(machine, providerconfig.MachineCreated, machineCreationSucceedReason, machineCreationSucceedMessage, corev1.ConditionTrue)
	if updateConditionError != nil {
		return a.handleMachineError(machine, updateConditionError, createEventAction)
	}
	machine = modMachine

	a.eventRecorder.Eventf(machine, corev1.EventTypeNormal, createEventAction, "Created Machine %v", machine.Name)
	return nil
}

func (a *Actuator) Exists(ctx context.Context, cluster *clusterv1.Cluster, machine *machinev1.Machine) (bool, error) {
	klog.Infof("%s: Checking if machine exists", machine.Name)

	providerSpec, err := providerConfigFromMachine(machine, a.codec)
	if err != nil {
		a.eventRecorder.Eventf(machine, corev1.EventTypeNormal, createEventAction, "Invalid machine %q configuration: %v", machine.Name, err)
		return false, fmt.Errorf("unable to decode machine provider config: %v", err)
	}

	credentialsSecret, err := a.getCredentialsSecret(machine, providerSpec)
	if err != nil {
		return false, fmt.Errorf("unable to get credentials secret: %v", err)
	}

	scope, err := newMachineScope(machineScopeParams{
		credentialsSecret: credentialsSecret,
		projectID:         providerSpec.ProjectID,
	})
	if err != nil {
		return false, fmt.Errorf(scopeFailFmt, machine.Name, err)
	}

	// The core machine controller calls exists() + create()/update() in the same reconciling operation.
	// If exists() would store machineSpec/status object then create()/update() would still receive the local version.
	// When create()/update() try to store machineSpec/status this might result in
	// "Operation cannot be fulfilled; the object has been modified; please apply your changes to the latest version and try again."
	// Therefore we don't close the scope here and we only store spec/status atomically either in create()/update()"
	return newReconciler(scope, a.coreClient).exists(machine, providerSpec)
}

func (a *Actuator) Update(ctx context.Context, cluster *clusterv1.Cluster, machine *machinev1.Machine) error {
	klog.Infof("%s: Updating machine", machine.Name)

	providerSpec, err := providerConfigFromMachine(machine, a.codec)
	if err != nil {
		a.eventRecorder.Eventf(machine, corev1.EventTypeNormal, createEventAction, "Invalid machine %q configuration: %v", machine.Name, err)
		return fmt.Errorf("unable to decode machine provider config: %v", err)
	}

	credentialsSecret, err := a.getCredentialsSecret(machine, providerSpec)
	if err != nil {
		return fmt.Errorf("unable to get credentials secret: %v", err)
	}

	scope, err := newMachineScope(machineScopeParams{
		credentialsSecret: credentialsSecret,
		projectID:         providerSpec.ProjectID,
	})
	if err != nil {
		fmtErr := fmt.Sprintf(scopeFailFmt, machine.Name, err)
		return a.handleMachineError(machine, fmt.Errorf(fmtErr), updateEventAction)
	}

	reconciler := newReconciler(scope, a.coreClient)
	instance, err := reconciler.update(machine, providerSpec)
	if instance != nil {
		modMachine, err := a.setMachineCloudProviderSpecifics(machine, instance)
		if err != nil {
			klog.Errorf("%s: error updating machine cloud provider specifics: %v", machine.Name, err)
		} else {
			machine = modMachine
		}

		modeMachine, err := a.updateStatus(ctx, machine, reconciler, instance)
		if err != nil {
			klog.Errorf("%s: error updating machine status: %v", machine.Name, err)
		} else {
			machine = modeMachine
		}

		modeMachine, err = a.updateProviderID(machine, scope)
		if err != nil {
			klog.Errorf("%s: error updating machine provider ID: %v", machine.Name, err)
		} else {
			machine = modeMachine
		}

		// Create can fail before machine created condition is set. Meantime,
		// instance in GCE can be already running so the condition will never
		// get set to true. Thus, when we get to Update op, we already know
		// the instance was succesfully created.
		modMachine, updateConditionError := a.updateMachineProviderConditions(machine, providerconfig.MachineCreated, machineCreationSucceedReason, machineCreationSucceedMessage, corev1.ConditionTrue)
		if updateConditionError != nil {
			return a.handleMachineError(machine, updateConditionError, createEventAction)
		}
		machine = modMachine
	}

	if err != nil {
		return a.handleMachineError(machine, err, updateEventAction)
	}

	a.eventRecorder.Eventf(machine, corev1.EventTypeNormal, updateEventAction, "Updated Machine %v", machine.Name)
	return nil
}

func (a *Actuator) Delete(ctx context.Context, cluster *clusterv1.Cluster, machine *machinev1.Machine) error {
	klog.Infof("%s: Deleting machine", machine.Name)

	providerSpec, err := providerConfigFromMachine(machine, a.codec)
	if err != nil {
		a.eventRecorder.Eventf(machine, corev1.EventTypeNormal, createEventAction, "Invalid machine %q configuration: %v", machine.Name, err)
		return fmt.Errorf("unable to decode machine provider config: %v", err)
	}

	credentialsSecret, err := a.getCredentialsSecret(machine, providerSpec)
	if err != nil {
		return fmt.Errorf("unable to get credentials secret: %v", err)
	}

	scope, err := newMachineScope(machineScopeParams{
		credentialsSecret: credentialsSecret,
		projectID:         providerSpec.ProjectID,
	})
	if err != nil {
		fmtErr := fmt.Sprintf(scopeFailFmt, machine.Name, err)
		return a.handleMachineError(machine, fmt.Errorf(fmtErr), deleteEventAction)
	}

	modMachine, err := a.setDeletingState(ctx, machine)
	if err != nil {
		klog.Errorf("unable to set machine deleting state: %v", err)
	} else {
		machine = modMachine
	}

	if err := newReconciler(scope, a.coreClient).delete(machine, providerSpec); err != nil {
		return a.handleMachineError(machine, err, deleteEventAction)
	}
	a.eventRecorder.Eventf(machine, corev1.EventTypeNormal, deleteEventAction, "Deleted machine %v", machine.Name)
	return nil
}

func (a *Actuator) updateMachineProviderConditions(machine *machinev1.Machine, conditionType providerconfig.GCPMachineProviderConditionType, reason string, msg string, status corev1.ConditionStatus) (*machinev1.Machine, error) {
	klog.Infof("%s: updating machine conditions", machine.Name)

	gcpStatus := &providerconfig.GCPMachineProviderStatus{}
	if err := a.codec.DecodeProviderStatus(machine.Status.ProviderStatus, gcpStatus); err != nil {
		klog.Errorf("%s: error decoding machine provider status: %v", machine.Name, err)
		return nil, err
	}

	gcpStatus.Conditions = reconcileProviderConditions(gcpStatus.Conditions, providerconfig.GCPMachineProviderCondition{
		Type:    conditionType,
		Status:  status,
		Reason:  reason,
		Message: msg,
	})

	modMachine, err := a.updateMachineStatus(machine, gcpStatus, nil)
	if err != nil {
		return nil, err
	}

	return modMachine, nil
}

func (a *Actuator) updateMachineStatus(machine *machinev1.Machine, gcpStatus *providerconfig.GCPMachineProviderStatus, networkAddresses []corev1.NodeAddress) (*machinev1.Machine, error) {
	gcpStatusRaw, err := a.codec.EncodeProviderStatus(gcpStatus)
	if err != nil {
		klog.Errorf("%s: error encoding AWS provider status: %v", machine.Name, err)
		return nil, err
	}

	machineCopy := machine.DeepCopy()
	machineCopy.Status.ProviderStatus = gcpStatusRaw
	if networkAddresses != nil {
		machineCopy.Status.Addresses = networkAddresses
	}

	oldGCPStatus := &providerconfig.GCPMachineProviderStatus{}
	if err := a.codec.DecodeProviderStatus(machine.Status.ProviderStatus, oldGCPStatus); err != nil {
		klog.Errorf("%s: error updating machine status: %v", machine.Name, err)
		return nil, err
	}

	if !equality.Semantic.DeepEqual(gcpStatus, oldGCPStatus) || !equality.Semantic.DeepEqual(machine.Status.Addresses, machineCopy.Status.Addresses) {
		klog.Infof("%s: machine status has changed, updating", machine.Name)
		time := metav1.Now()
		machineCopy.Status.LastUpdated = &time

		if err := a.coreClient.Status().Update(context.Background(), machineCopy); err != nil {
			klog.Errorf("%s: error updating machine status: %v", machine.Name, err)
			return nil, err
		}
		return machineCopy, nil
	}

	klog.Infof("%s: status unchanged", machine.Name)
	return machine, nil
}

// providerConfigFromMachine gets the machine provider config MachineSetSpec from the
// specified cluster-api MachineSpec.
func providerConfigFromMachine(machine *machinev1.Machine, codec *providerconfig.GCPProviderConfigCodec) (*providerconfig.GCPMachineProviderSpec, error) {
	if machine.Spec.ProviderSpec.Value == nil {
		return nil, fmt.Errorf("unable to find machine provider config: Spec.ProviderSpec.Value is not set")
	}

	var config providerconfig.GCPMachineProviderSpec
	if err := codec.DecodeProviderSpec(&machine.Spec.ProviderSpec, &config); err != nil {
		return nil, err
	}
	return &config, nil
}

// updateStatus calculates the new machine status, checks if anything has changed, and updates if so.
func (a *Actuator) updateStatus(ctx context.Context, machine *machinev1.Machine, reconciler *Reconciler, instance *compute.Instance) (*machinev1.Machine, error) {
	klog.Infof("%s: Updating status", machine.Name)

	gcpStatus := &providerconfig.GCPMachineProviderStatus{}
	if err := a.codec.DecodeProviderStatus(machine.Status.ProviderStatus, gcpStatus); err != nil {
		klog.Errorf("%s: Error decoding machine provider status: %v", machine.Name, err)
		return nil, err
	}

	providerSpec, err := providerConfigFromMachine(machine, a.codec)
	if err != nil {
		return nil, err
	}

	networkAddresses, err := reconciler.getNetworkAddresses(ctx, instance, machine, providerSpec.Zone)
	if err != nil {
		return nil, err
	}

	gcpStatus.InstanceState = &instance.Status
	gcpStatus.InstanceID = &instance.Name

	klog.Infof("%s: finished calculating GCP status", machine.Name)

	modMachine, err := a.updateMachineStatus(machine, gcpStatus, networkAddresses)
	if err != nil {
		return nil, err
	}

	return modMachine, nil
}

func (a *Actuator) setDeletingState(ctx context.Context, machine *machinev1.Machine) (*machinev1.Machine, error) {
	// Getting a vm object does not work here so let's assume
	// an instance is really being deleted
	gcpStatus := &providerconfig.GCPMachineProviderStatus{}
	if err := a.codec.DecodeProviderStatus(machine.Status.ProviderStatus, gcpStatus); err != nil {
		klog.Errorf("%s: Error decoding machine provider status: %v", machine.Name, err)
		return nil, err
	}

	deleting := "DELETING"
	gcpStatus.InstanceState = &deleting

	modMachine, err := a.updateMachineStatus(machine, gcpStatus, nil)
	if err != nil {
		return nil, fmt.Errorf("%s: error updating machine status: %v", modMachine.Name, err)
	}

	modMachine.Annotations[machinecontroller.MachineInstanceStateAnnotationName] = deleting

	if err := a.coreClient.Update(ctx, modMachine); err != nil {
		return nil, fmt.Errorf("%s: error updating machine spec: %v", modMachine.Name, err)
	}

	return modMachine, nil
}

// updateProviderID adds providerID in the machine spec
func (a *Actuator) updateProviderID(machine *machinev1.Machine, scope *machineScope) (*machinev1.Machine, error) {
	providerSpec, err := providerConfigFromMachine(machine, a.codec)
	if err != nil {
		return nil, err
	}

	// https://github.com/kubernetes/kubernetes/blob/8765fa2e48974e005ad16e65cb5c3acf5acff17b/staging/src/k8s.io/legacy-cloud-providers/gce/gce_util.go#L204
	providerID := fmt.Sprintf("gce://%s/%s/%s", scope.projectID, providerSpec.Zone, machine.Name)
	klog.Infof("%s: setting ProviderID %s", machine.Name, providerID)

	machineCopy := machine.DeepCopy()
	machineCopy.Spec.ProviderID = &providerID

	if machine.Spec.ProviderID != nil && *machine.Spec.ProviderID == providerID {
		return machine, nil
	}

	if err := a.coreClient.Update(context.Background(), machineCopy); err != nil {
		return nil, fmt.Errorf("%s: error updating machine spec ProviderID: %v", machineCopy.Name, err)
	}

	return machineCopy, nil
}

func (a *Actuator) setMachineCloudProviderSpecifics(machine *machinev1.Machine, instance *compute.Instance) (*machinev1.Machine, error) {
	machineCopy := machine.DeepCopy()

	if machineCopy.Labels == nil {
		machineCopy.Labels = make(map[string]string)
	}

	if machineCopy.Annotations == nil {
		machineCopy.Annotations = make(map[string]string)
	}

	providerSpec, err := providerConfigFromMachine(machine, a.codec)
	if err != nil {
		return nil, err
	}

	machineCopy.Annotations[machinecontroller.MachineInstanceStateAnnotationName] = instance.Status
	// TODO(jchaloup): detect all three from instance rather than
	// always assuming it's the same as what is specified in the provider spec
	machineCopy.Labels[machinecontroller.MachineInstanceTypeLabelName] = providerSpec.MachineType
	machineCopy.Labels[machinecontroller.MachineRegionLabelName] = providerSpec.Region
	machineCopy.Labels[machinecontroller.MachineAZLabelName] = providerSpec.Zone

	if equality.Semantic.DeepEqual(machine.Labels, machineCopy.Labels) && equality.Semantic.DeepEqual(machine.Annotations, machineCopy.Annotations) {
		return machine, nil
	}

	if err := a.coreClient.Update(context.Background(), machineCopy); err != nil {
		return nil, fmt.Errorf("%s: error updating machine cloud provider specifics: %v", machineCopy.Name, err)
	}

	return machineCopy, nil
}
