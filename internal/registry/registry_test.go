package registry_test

import (
	"fmt"
	"sort"
	"sync"
	"testing"

	edgeerrors "github.com/Gorakhnath-R-Patil/EdgeMesh/internal/errors"
	"github.com/Gorakhnath-R-Patil/EdgeMesh/internal/registry"

	meshv1alpha1 "github.com/Gorakhnath-R-Patil/EdgeMesh/gen/go/edgemesh/mesh/v1alpha1"
)

func testService(name string) *meshv1alpha1.Service {
	return &meshv1alpha1.Service{Name: name}
}

func testEndpoint(service, id string) *meshv1alpha1.Endpoint {
	return &meshv1alpha1.Endpoint{
		Id:          id,
		ServiceName: service,
		Address:     "10.0.0.1",
		Port:        8080,
	}
}

func wantNotFound(t *testing.T, err error) {
	t.Helper()
	if !edgeerrors.Is(err, edgeerrors.ErrNotFound) {
		t.Fatalf("error = %v, want it to wrap ErrNotFound", err)
	}
}

// ---- Service lifecycle ----

func TestRegisterServiceThenLookupHasNoEndpoints(t *testing.T) {
	r := registry.New()
	if err := r.RegisterService(testService("payment")); err != nil {
		t.Fatalf("RegisterService() error = %v, want nil", err)
	}

	eps, err := r.Lookup("payment")
	if err != nil {
		t.Fatalf("Lookup() error = %v, want nil", err)
	}
	if len(eps) != 0 {
		t.Errorf("Lookup() returned %d endpoints, want 0 for a freshly registered service", len(eps))
	}
}

func TestLookupUnknownServiceReturnsNotFound(t *testing.T) {
	r := registry.New()
	_, err := r.Lookup("does-not-exist")
	wantNotFound(t, err)
}

func TestRegisterServiceRejectsInvalidService(t *testing.T) {
	r := registry.New()
	err := r.RegisterService(&meshv1alpha1.Service{})
	if !edgeerrors.Is(err, edgeerrors.ErrInvalidConfig) {
		t.Fatalf("RegisterService() error = %v, want it to wrap ErrInvalidConfig", err)
	}
}

func TestRegisterServiceUpsertReplacesMetadataAndKeepsEndpoints(t *testing.T) {
	r := registry.New()
	must(t, r.RegisterService(&meshv1alpha1.Service{Name: "payment", Namespace: "default"}))
	must(t, r.RegisterEndpoint(testEndpoint("payment", "payment-1")))

	// Re-register with different metadata.
	must(t, r.RegisterService(&meshv1alpha1.Service{Name: "payment", Namespace: "prod"}))

	services := r.ListServices()
	if len(services) != 1 || services[0].GetNamespace() != "prod" {
		t.Fatalf("ListServices() = %+v, want one service with namespace %q", services, "prod")
	}

	eps, err := r.Lookup("payment")
	if err != nil {
		t.Fatalf("Lookup() error = %v, want nil", err)
	}
	if len(eps) != 1 {
		t.Fatalf("Lookup() returned %d endpoints, want 1 (re-registering a service must not drop its endpoints)", len(eps))
	}
}

func TestDeregisterServiceCascadesToEndpoints(t *testing.T) {
	r := registry.New()
	must(t, r.RegisterService(testService("payment")))
	must(t, r.RegisterEndpoint(testEndpoint("payment", "payment-1")))
	must(t, r.RegisterEndpoint(testEndpoint("payment", "payment-2")))

	if err := r.DeregisterService("payment"); err != nil {
		t.Fatalf("DeregisterService() error = %v, want nil", err)
	}

	if _, err := r.Lookup("payment"); err == nil {
		t.Fatal("Lookup() after DeregisterService() error = nil, want ErrNotFound")
	}

	// Re-registering the service starts with a clean endpoint list.
	must(t, r.RegisterService(testService("payment")))
	eps, err := r.Lookup("payment")
	if err != nil {
		t.Fatalf("Lookup() error = %v, want nil", err)
	}
	if len(eps) != 0 {
		t.Errorf("Lookup() returned %d endpoints, want 0 after cascade-delete and re-registration", len(eps))
	}
}

func TestDeregisterServiceUnknownReturnsNotFound(t *testing.T) {
	r := registry.New()
	wantNotFound(t, r.DeregisterService("does-not-exist"))
}

func TestListServicesReturnsAllRegistered(t *testing.T) {
	r := registry.New()
	must(t, r.RegisterService(testService("payment")))
	must(t, r.RegisterService(testService("order")))

	names := serviceNames(r.ListServices())
	if len(names) != 2 || !contains(names, "payment") || !contains(names, "order") {
		t.Errorf("ListServices() = %v, want [order payment]", names)
	}
}

// ---- Endpoint lifecycle ----

func TestRegisterEndpointRequiresServiceRegisteredFirst(t *testing.T) {
	r := registry.New()
	err := r.RegisterEndpoint(testEndpoint("payment", "payment-1"))
	wantNotFound(t, err)
}

func TestRegisterEndpointThenLookupReturnsIt(t *testing.T) {
	r := registry.New()
	must(t, r.RegisterService(testService("payment")))
	must(t, r.RegisterEndpoint(testEndpoint("payment", "payment-1")))

	eps, err := r.Lookup("payment")
	if err != nil {
		t.Fatalf("Lookup() error = %v, want nil", err)
	}
	if len(eps) != 1 || eps[0].GetId() != "payment-1" {
		t.Fatalf("Lookup() = %+v, want one endpoint payment-1", eps)
	}
}

func TestRegisterDuplicateEndpointReturnsAlreadyExists(t *testing.T) {
	r := registry.New()
	must(t, r.RegisterService(testService("payment")))
	must(t, r.RegisterEndpoint(testEndpoint("payment", "payment-1")))

	err := r.RegisterEndpoint(testEndpoint("payment", "payment-1"))
	if !edgeerrors.Is(err, edgeerrors.ErrAlreadyExists) {
		t.Fatalf("error = %v, want it to wrap ErrAlreadyExists", err)
	}
}

func TestRegisterEndpointRejectsInvalidEndpoint(t *testing.T) {
	r := registry.New()
	must(t, r.RegisterService(testService("payment")))

	err := r.RegisterEndpoint(&meshv1alpha1.Endpoint{ServiceName: "payment"}) // missing id/address/port
	if !edgeerrors.Is(err, edgeerrors.ErrInvalidConfig) {
		t.Fatalf("error = %v, want it to wrap ErrInvalidConfig", err)
	}
}

func TestUpdateEndpointModifiesFields(t *testing.T) {
	r := registry.New()
	must(t, r.RegisterService(testService("payment")))
	must(t, r.RegisterEndpoint(testEndpoint("payment", "payment-1")))

	updated := testEndpoint("payment", "payment-1")
	updated.Health = meshv1alpha1.HealthState_HEALTH_STATE_UNHEALTHY
	updated.Weight = 5
	if err := r.UpdateEndpoint(updated); err != nil {
		t.Fatalf("UpdateEndpoint() error = %v, want nil", err)
	}

	eps, _ := r.Lookup("payment")
	if len(eps) != 1 {
		t.Fatalf("Lookup() returned %d endpoints, want 1 (update must not add a new one)", len(eps))
	}
	if eps[0].GetHealth() != meshv1alpha1.HealthState_HEALTH_STATE_UNHEALTHY {
		t.Errorf("Health = %v, want UNHEALTHY after update", eps[0].GetHealth())
	}
	if eps[0].GetWeight() != 5 {
		t.Errorf("Weight = %d, want 5 after update", eps[0].GetWeight())
	}
}

func TestUpdateEndpointUnknownEndpointReturnsNotFound(t *testing.T) {
	r := registry.New()
	must(t, r.RegisterService(testService("payment")))

	err := r.UpdateEndpoint(testEndpoint("payment", "payment-1"))
	wantNotFound(t, err)
}

func TestUpdateEndpointUnknownServiceReturnsNotFound(t *testing.T) {
	r := registry.New()
	err := r.UpdateEndpoint(testEndpoint("payment", "payment-1"))
	wantNotFound(t, err)
}

func TestDeregisterEndpointRemovesIt(t *testing.T) {
	r := registry.New()
	must(t, r.RegisterService(testService("payment")))
	must(t, r.RegisterEndpoint(testEndpoint("payment", "payment-1")))
	must(t, r.RegisterEndpoint(testEndpoint("payment", "payment-2")))

	if err := r.DeregisterEndpoint("payment", "payment-1"); err != nil {
		t.Fatalf("DeregisterEndpoint() error = %v, want nil", err)
	}

	eps, _ := r.Lookup("payment")
	if len(eps) != 1 || eps[0].GetId() != "payment-2" {
		t.Fatalf("Lookup() = %+v, want only payment-2 left", eps)
	}
}

func TestDeregisterEndpointUnknownEndpointReturnsNotFound(t *testing.T) {
	r := registry.New()
	must(t, r.RegisterService(testService("payment")))
	wantNotFound(t, r.DeregisterEndpoint("payment", "payment-1"))
}

func TestDeregisterEndpointUnknownServiceReturnsNotFound(t *testing.T) {
	r := registry.New()
	wantNotFound(t, r.DeregisterEndpoint("payment", "payment-1"))
}

// ---- Defensive copying ----

func TestLookupReturnsDefensiveCopies(t *testing.T) {
	r := registry.New()
	must(t, r.RegisterService(testService("payment")))
	must(t, r.RegisterEndpoint(testEndpoint("payment", "payment-1")))

	eps, _ := r.Lookup("payment")
	eps[0].Address = "mutated"

	again, _ := r.Lookup("payment")
	if again[0].GetAddress() == "mutated" {
		t.Fatal("mutating a Lookup() result mutated the registry's internal state")
	}
}

func TestRegisterEndpointCopiesInput(t *testing.T) {
	r := registry.New()
	must(t, r.RegisterService(testService("payment")))

	ep := testEndpoint("payment", "payment-1")
	must(t, r.RegisterEndpoint(ep))
	ep.Address = "mutated-after-register"

	stored, _ := r.Lookup("payment")
	if stored[0].GetAddress() == "mutated-after-register" {
		t.Fatal("mutating the caller's endpoint after RegisterEndpoint() mutated the registry's internal state")
	}
}

func TestListServicesReturnsDefensiveCopies(t *testing.T) {
	r := registry.New()
	must(t, r.RegisterService(testService("payment")))

	services := r.ListServices()
	services[0].Namespace = "mutated"

	again := r.ListServices()
	if again[0].GetNamespace() == "mutated" {
		t.Fatal("mutating a ListServices() result mutated the registry's internal state")
	}
}

// ---- Concurrency ----

func TestConcurrentRegisterEndpointsDistinctIDs(t *testing.T) {
	r := registry.New()
	must(t, r.RegisterService(testService("payment")))

	const n = 200
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("payment-%d", i)
			if err := r.RegisterEndpoint(testEndpoint("payment", id)); err != nil {
				t.Errorf("RegisterEndpoint(%s) error = %v, want nil", id, err)
			}
		}(i)
	}
	wg.Wait()

	eps, err := r.Lookup("payment")
	if err != nil {
		t.Fatalf("Lookup() error = %v, want nil", err)
	}
	if len(eps) != n {
		t.Fatalf("Lookup() returned %d endpoints, want %d", len(eps), n)
	}
}

func TestConcurrentReadersAndWritersDoNotRace(t *testing.T) {
	r := registry.New()
	must(t, r.RegisterService(testService("payment")))
	must(t, r.RegisterEndpoint(testEndpoint("payment", "payment-1")))

	const iterations = 500
	var wg sync.WaitGroup
	wg.Add(4)

	// Writer: repeatedly updates the one endpoint's weight.
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			ep := testEndpoint("payment", "payment-1")
			ep.Weight = uint32(i%100) + 1
			_ = r.UpdateEndpoint(ep) // may race benignly with deregistration below; asserting "no crash", not a specific value
		}
	}()

	// A second writer: registers and deregisters a churn endpoint.
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			_ = r.RegisterEndpoint(testEndpoint("payment", "churn"))
			_ = r.DeregisterEndpoint("payment", "churn")
		}
	}()

	// Two readers.
	for g := 0; g < 2; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				if _, err := r.Lookup("payment"); err != nil {
					t.Errorf("Lookup() error = %v, want nil (service is never deregistered in this test)", err)
				}
				r.ListServices()
			}
		}()
	}

	wg.Wait()

	// The registry must still be in a well-formed state: the permanent
	// endpoint is present, the churn endpoint is not (its last
	// operation in each goroutine iteration is always a deregister).
	eps, err := r.Lookup("payment")
	if err != nil {
		t.Fatalf("Lookup() error = %v, want nil", err)
	}
	var sawPermanent bool
	for _, ep := range eps {
		if ep.GetId() == "payment-1" {
			sawPermanent = true
		}
	}
	if !sawPermanent {
		t.Error("Lookup() no longer contains payment-1 after concurrent churn")
	}
}

func TestConcurrentServiceRegistrationAndDeregistration(t *testing.T) {
	r := registry.New()

	const iterations = 300
	var wg sync.WaitGroup
	wg.Add(3)

	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			_ = r.RegisterService(testService("flaky"))
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			_ = r.DeregisterService("flaky") // ErrNotFound races are expected and ignored
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			_, _ = r.Lookup("flaky") // ErrNotFound races are expected and ignored
			r.ListServices()
		}
	}()

	wg.Wait()
	// Reaching here without a panic/race (checked by `go test -race` in
	// CI) is the assertion: register/deregister/lookup churn on the
	// same service name must never corrupt registry state.
}

// ---- helpers ----

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func serviceNames(services []*meshv1alpha1.Service) []string {
	names := make([]string, len(services))
	for i, s := range services {
		names[i] = s.GetName()
	}
	sort.Strings(names)
	return names
}

func contains(names []string, name string) bool {
	for _, n := range names {
		if n == name {
			return true
		}
	}
	return false
}
