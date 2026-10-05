package scoring

import (
	"math"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestBuiltinsAreVersionedAndStable(t *testing.T) {
	got := Builtins().List()
	want := []string{
		"decay@1",
		"field-relative@1",
		"first-solve-time@1",
		"logarithmic@1",
		"solve-threshold@1",
		"static@1",
		"steep@1",
	}
	keys := make([]string, len(got))
	for index, descriptor := range got {
		keys[index] = providerKey(descriptor.Provider, descriptor.Revision)
	}
	if !reflect.DeepEqual(keys, want) {
		t.Fatalf("providers = %v, want %v", keys, want)
	}
}

func TestProviderReferenceValues(t *testing.T) {
	registry := Builtins()
	context := Context{MinPoints: 100, MaxPoints: 500, Solves: 20, MaxSolves: 100}
	cases := []struct {
		definition Definition
		want       int
	}{
		{Definition{Provider: "static", Revision: 1}, 500},
		{Definition{Provider: "decay", Revision: 1}, 245},
		{Definition{Provider: "logarithmic", Revision: 1}, 270},
		{Definition{Provider: "steep", Revision: 1}, 196},
		{Definition{Provider: "solve-threshold", Revision: 1}, 0},
		{Definition{Provider: "field-relative", Revision: 1}, 403},
	}
	for _, test := range cases {
		got, err := Calculate(registry, test.definition, context)
		if err != nil {
			t.Fatalf("%s: %v", test.definition.Provider, err)
		}
		if got != test.want {
			t.Errorf("%s = %d, want %d", test.definition.Provider, got, test.want)
		}
	}
}

func TestDecayProvidersAreBoundedAndMonotonic(t *testing.T) {
	registry := Builtins()
	for _, name := range []string{"decay", "logarithmic", "steep", "field-relative"} {
		last := math.MaxInt
		for solves := 0; solves <= 500; solves++ {
			score, err := Calculate(registry, Definition{Provider: name, Revision: 1}, Context{
				MinPoints: 100,
				MaxPoints: 500,
				Solves:    solves,
				MaxSolves: 500,
			})
			if err != nil {
				t.Fatalf("%s solve %d: %v", name, solves, err)
			}
			if score < 100 || score > 500 {
				t.Fatalf("%s solve %d = %d, outside [100,500]", name, solves, score)
			}
			if score > last {
				t.Fatalf("%s increased from %d to %d at solve %d", name, last, score, solves)
			}
			last = score
		}
	}
}

func TestFirstSolveTime(t *testing.T) {
	registry := Builtins()
	start := time.Date(2026, time.October, 5, 0, 0, 0, 0, time.UTC)
	end := start.Add(10 * time.Hour)
	early := start.Add(time.Hour)
	late := start.Add(9 * time.Hour)
	definition := Definition{Provider: "first-solve-time", Revision: 1}
	earlyScore, err := Calculate(registry, definition, Context{MinPoints: 100, MaxPoints: 500, EventStart: start, EventEnd: end, FirstSolveAt: &early})
	if err != nil {
		t.Fatal(err)
	}
	lateScore, err := Calculate(registry, definition, Context{MinPoints: 100, MaxPoints: 500, EventStart: start, EventEnd: end, FirstSolveAt: &late})
	if err != nil {
		t.Fatal(err)
	}
	if earlyScore >= lateScore {
		t.Fatalf("early score %d must be lower than late score %d", earlyScore, lateScore)
	}
	unsolved, err := Calculate(registry, definition, Context{MinPoints: 100, MaxPoints: 500})
	if err != nil || unsolved != 500 {
		t.Fatalf("unsolved = %d, %v", unsolved, err)
	}
}

func TestDefinitionsRejectInvalidState(t *testing.T) {
	registry := Builtins()
	cases := []Definition{
		{Provider: "missing", Revision: 1},
		{Provider: "static", Revision: 0},
		{Provider: "static", Revision: 1, Options: map[string]float64{"extra": 1}},
		{Provider: "first-solve-time", Revision: 1, Options: map[string]float64{"maximum_score_time": 0}},
		{Provider: "solve-threshold", Revision: 1, Options: map[string]float64{"awarded_solves": 1.5}},
		{Provider: "decay", Revision: 1, Options: map[string]float64{"bad": math.NaN()}},
	}
	for _, definition := range cases {
		if _, err := registry.Compile(definition); err == nil {
			t.Errorf("expected compile error for %#v", definition)
		}
	}
	if _, err := Calculate(registry, Definition{Provider: "static", Revision: 1}, Context{MinPoints: 500, MaxPoints: 100}); err == nil {
		t.Fatal("expected invalid bounds error")
	}
}

func TestRegistryRejectsDuplicateProvider(t *testing.T) {
	registry := NewRegistry()
	descriptor := Descriptor{Provider: "custom", Revision: 1, Label: "Custom"}
	registry.MustRegister(descriptor, newStatic)
	if err := registry.Register(descriptor, newStatic); err == nil {
		t.Fatal("expected duplicate provider error")
	}
}

func TestDefinitionFingerprintIsCanonicalAndValidated(t *testing.T) {
	registry := Builtins()
	first, err := registry.Fingerprint(Definition{Provider: "first-solve-time", Revision: 1, Options: map[string]float64{"maximum_score_time": 0.8}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := registry.Fingerprint(Definition{Provider: "first-solve-time", Revision: 1, Options: map[string]float64{"maximum_score_time": 0.8}})
	if err != nil {
		t.Fatal(err)
	}
	if first != second || len(first) != len("sha256:")+64 {
		t.Fatalf("fingerprints = %q and %q", first, second)
	}
	if _, err := registry.Fingerprint(Definition{Provider: "missing", Revision: 1}); err == nil {
		t.Fatal("expected unknown provider error")
	}
}

func TestRegistrySupportsConcurrentDiscoveryAndCompile(t *testing.T) {
	registry := Builtins()
	var wait sync.WaitGroup
	for index := 0; index < 32; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if len(registry.List()) != 7 {
				t.Errorf("provider list changed during concurrent read")
			}
			provider, err := registry.Compile(Definition{Provider: "decay", Revision: 1})
			if err != nil {
				t.Errorf("compile: %v", err)
				return
			}
			if _, err := provider.Calculate(Context{MinPoints: 100, MaxPoints: 500, Solves: 10}); err != nil {
				t.Errorf("calculate: %v", err)
			}
		}()
	}
	wait.Wait()
}

func BenchmarkBuiltInScoring(b *testing.B) {
	registry := Builtins()
	provider, err := registry.Compile(Definition{Provider: "decay", Revision: 1})
	if err != nil {
		b.Fatal(err)
	}
	context := Context{MinPoints: 100, MaxPoints: 500, Solves: 42, MaxSolves: 3000}
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		if _, err := provider.Calculate(context); err != nil {
			b.Fatal(err)
		}
	}
}
