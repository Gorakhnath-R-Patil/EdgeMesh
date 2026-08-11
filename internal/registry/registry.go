// Package registry implements EdgeMesh's in-memory service registry:
// the "service -> endpoint list" mapping shown in the architecture
// diagrams. It is the single source of truth the data plane will read
// from once discovery is wired into the request path (a later
// development phase); today it only stores what's explicitly
// registered, in memory, with no persistence and no discovery source.
//
// A Service must be registered before endpoints can be registered under
// it — RegisterEndpoint returns an error wrapping ErrNotFound
// otherwise — so a service's endpoint list is always anchored to a real
// Service declaration rather than inferred from whichever endpoint
// happened to register first. Deregistering a Service cascades to
// every endpoint registered under it.
//
// Registry is safe for concurrent use: a single sync.RWMutex guards all
// state, and every method that stores or returns a protobuf message
// does so through a defensive proto.Clone. That means a caller can
// never mutate the registry's internal state through a pointer it was
// handed back, and the registry can never be corrupted by a caller
// mutating a message after passing it in.
package registry

import (
	"fmt"
	"sync"

	"google.golang.org/protobuf/proto"

	edgeerrors "github.com/Gorakhnath-R-Patil/EdgeMesh/internal/errors"
	"github.com/Gorakhnath-R-Patil/EdgeMesh/internal/mesh"

	meshv1alpha1 "github.com/Gorakhnath-R-Patil/EdgeMesh/gen/go/edgemesh/mesh/v1alpha1"
)

// Registry is an in-memory, concurrency-safe store of Services and the
// Endpoints registered under them. The zero value is not usable; build
// one with New.
type Registry struct {
	mu       sync.RWMutex
	services map[string]*serviceEntry
}

// serviceEntry holds one Service's metadata and its endpoints, keyed by
// Endpoint.Id.
type serviceEntry struct {
	service   *meshv1alpha1.Service
	endpoints map[string]*meshv1alpha1.Endpoint
}

// New returns an empty Registry.
func New() *Registry {
	return &Registry{services: make(map[string]*serviceEntry)}
}

// RegisterService adds svc to the registry, or replaces the stored
// metadata for an existing service of the same name (an upsert —
// re-registering a Service to update its ports/labels is a normal
// control-plane operation). Endpoints already registered under that
// name are left untouched.
func (r *Registry) RegisterService(svc *meshv1alpha1.Service) error {
	const op = "registry.RegisterService"
	if err := mesh.ValidateService(svc); err != nil {
		return edgeerrors.Wrap(op, err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	entry, ok := r.services[svc.GetName()]
	if !ok {
		entry = &serviceEntry{endpoints: make(map[string]*meshv1alpha1.Endpoint)}
		r.services[svc.GetName()] = entry
	}
	entry.service = cloneService(svc)
	return nil
}

// DeregisterService removes name and every endpoint registered under
// it. It returns an error wrapping ErrNotFound if name was never
// registered.
func (r *Registry) DeregisterService(name string) error {
	const op = "registry.DeregisterService"

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.services[name]; !ok {
		return edgeerrors.Wrap(op, fmt.Errorf("%w: service %q", edgeerrors.ErrNotFound, name))
	}
	delete(r.services, name)
	return nil
}

// ListServices returns every registered Service, in no particular
// order.
func (r *Registry) ListServices() []*meshv1alpha1.Service {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]*meshv1alpha1.Service, 0, len(r.services))
	for _, entry := range r.services {
		out = append(out, cloneService(entry.service))
	}
	return out
}

// RegisterEndpoint adds ep under its ServiceName, which must already be
// registered (an error wrapping ErrNotFound otherwise). It returns an
// error wrapping ErrAlreadyExists if an endpoint with the same Id is
// already registered under that service — call UpdateEndpoint to
// modify an existing one instead.
func (r *Registry) RegisterEndpoint(ep *meshv1alpha1.Endpoint) error {
	const op = "registry.RegisterEndpoint"
	if err := mesh.ValidateEndpoint(ep); err != nil {
		return edgeerrors.Wrap(op, err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	entry, ok := r.services[ep.GetServiceName()]
	if !ok {
		return edgeerrors.Wrap(op, fmt.Errorf("%w: service %q", edgeerrors.ErrNotFound, ep.GetServiceName()))
	}
	if _, exists := entry.endpoints[ep.GetId()]; exists {
		return edgeerrors.Wrap(op, fmt.Errorf("%w: endpoint %q on service %q", edgeerrors.ErrAlreadyExists, ep.GetId(), ep.GetServiceName()))
	}
	entry.endpoints[ep.GetId()] = cloneEndpoint(ep)
	return nil
}

// UpdateEndpoint replaces the stored endpoint sharing ep's Id and
// ServiceName with ep — the mechanism for changes like a health-check
// result or a weight adjustment that don't warrant a full
// deregister/register cycle. It returns an error wrapping ErrNotFound
// if either the service or the endpoint isn't currently registered.
func (r *Registry) UpdateEndpoint(ep *meshv1alpha1.Endpoint) error {
	const op = "registry.UpdateEndpoint"
	if err := mesh.ValidateEndpoint(ep); err != nil {
		return edgeerrors.Wrap(op, err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	entry, ok := r.services[ep.GetServiceName()]
	if !ok {
		return edgeerrors.Wrap(op, fmt.Errorf("%w: service %q", edgeerrors.ErrNotFound, ep.GetServiceName()))
	}
	if _, exists := entry.endpoints[ep.GetId()]; !exists {
		return edgeerrors.Wrap(op, fmt.Errorf("%w: endpoint %q on service %q", edgeerrors.ErrNotFound, ep.GetId(), ep.GetServiceName()))
	}
	entry.endpoints[ep.GetId()] = cloneEndpoint(ep)
	return nil
}

// DeregisterEndpoint removes the endpoint identified by endpointID from
// serviceName. It returns an error wrapping ErrNotFound if either the
// service or the endpoint isn't currently registered.
func (r *Registry) DeregisterEndpoint(serviceName, endpointID string) error {
	const op = "registry.DeregisterEndpoint"

	r.mu.Lock()
	defer r.mu.Unlock()

	entry, ok := r.services[serviceName]
	if !ok {
		return edgeerrors.Wrap(op, fmt.Errorf("%w: service %q", edgeerrors.ErrNotFound, serviceName))
	}
	if _, exists := entry.endpoints[endpointID]; !exists {
		return edgeerrors.Wrap(op, fmt.Errorf("%w: endpoint %q on service %q", edgeerrors.ErrNotFound, endpointID, serviceName))
	}
	delete(entry.endpoints, endpointID)
	return nil
}

// Lookup returns every endpoint currently registered under
// serviceName, in no particular order — selecting among them is a
// routing-strategy concern (later development phases), not the
// registry's. It returns an error wrapping ErrNotFound if serviceName
// itself was never registered; a registered service with no live
// endpoints returns an empty, non-error slice, since "no instances are
// currently running" is a valid and important state to distinguish
// from "no such service".
func (r *Registry) Lookup(serviceName string) ([]*meshv1alpha1.Endpoint, error) {
	const op = "registry.Lookup"

	r.mu.RLock()
	defer r.mu.RUnlock()

	entry, ok := r.services[serviceName]
	if !ok {
		return nil, edgeerrors.Wrap(op, fmt.Errorf("%w: service %q", edgeerrors.ErrNotFound, serviceName))
	}

	out := make([]*meshv1alpha1.Endpoint, 0, len(entry.endpoints))
	for _, ep := range entry.endpoints {
		out = append(out, cloneEndpoint(ep))
	}
	return out, nil
}

func cloneService(s *meshv1alpha1.Service) *meshv1alpha1.Service {
	return proto.Clone(s).(*meshv1alpha1.Service)
}

func cloneEndpoint(e *meshv1alpha1.Endpoint) *meshv1alpha1.Endpoint {
	return proto.Clone(e).(*meshv1alpha1.Endpoint)
}
