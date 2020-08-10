package termination

import (
	"context"
	"fmt"
	"sync"

	"cloud.google.com/go/compute/metadata"
	"github.com/go-logr/logr"
	machinev1 "github.com/openshift/machine-api-operator/pkg/apis/machine/v1beta1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	gcpTerminationEndpointSuffix = "instance/preempted"
)

// Handler represents a handler that will run to check the termination
// notice endpoint and delete Machine's if the instance termination notice is fulfilled.
type Handler interface {
	Run(stop <-chan struct{}) error
}

// NewHandler constructs a new Handler
func NewHandler(logger logr.Logger, cfg *rest.Config, namespace, nodeName string) (Handler, error) {
	machinev1.AddToScheme(scheme.Scheme)
	c, err := client.New(cfg, client.Options{Scheme: scheme.Scheme})
	if err != nil {
		return nil, fmt.Errorf("error creating client: %v", err)
	}

	logger = logger.WithValues("node", nodeName, "namespace", namespace)

	h := &handler{
		client:    c,
		nodeName:  nodeName,
		namespace: namespace,
		log:       logger,
	}
	return h, nil
}

// handler implements the logic to check the termination endpoint and delete the
// machine associated with the node
type handler struct {
	ctx       context.Context
	client    client.Client
	nodeName  string
	namespace string
	log       logr.Logger
	machine   *machinev1.Machine
}

// Run starts the handler and runs the termination logic
func (h *handler) Run(stop <-chan struct{}) error {
	ctx, cancel := context.WithCancel(context.Background())
	h.ctx = ctx

	errs := make(chan error, 1)
	wg := &sync.WaitGroup{}
	wg.Add(1)

	go func() {
		defer wg.Done()
		errs <- h.run()
	}()

	select {
	case <-stop:
		cancel()
		// Wait for run to stop
		wg.Wait()
		return nil
	case err := <-errs:
		cancel()
		return err
	}
}

func (h *handler) run() error {
	machine, err := h.getMachineForNode()
	if err != nil {
		return fmt.Errorf("error fetching machine for node (%q): %w", h.nodeName, err)
	}
	h.machine = machine

	h.log = h.log.WithValues("machine", machine.Name)
	h.log.V(1).Info("Monitoring node for machine")

	// Use server-side wait.
	err = metadata.Subscribe(gcpTerminationEndpointSuffix, h.markForDeletion)
	if err != nil {
		return fmt.Errorf("Error waiting for instance termination (%q): %w", h.nodeName, err)
	}

	return nil
}

// According to Subscribe documentation:
// "Subscribe calls fn with the latest metadata value indicated by the provided
// suffix. If the metadata value is deleted, fn is called with the empty string
// and ok false."
func (h *handler) markForDeletion(markedForDeletion string, instanceMetaFound bool) error {
	if instanceMetaFound && markedForDeletion != "TRUE" {
		return nil
	}
	h.log.V(1).Info("Instance marked for termination, deleting Machine")
	if err := h.client.Delete(h.ctx, h.machine); err != nil {
		return fmt.Errorf("error deleting machine: %w", err)
	}
	return nil
}

// getMachineForNodeName finds the Machine associated with the Node name given
func (h *handler) getMachineForNode() (*machinev1.Machine, error) {
	machineList := &machinev1.MachineList{}
	err := h.client.List(h.ctx, machineList, client.InNamespace(h.namespace))
	if err != nil {
		return nil, fmt.Errorf("error listing machines: %w", err)
	}

	for _, machine := range machineList.Items {
		if machine.Status.NodeRef != nil && machine.Status.NodeRef.Name == h.nodeName {
			return &machine, nil
		}
	}

	return nil, fmt.Errorf("machine not found for node %q", h.nodeName)
}
