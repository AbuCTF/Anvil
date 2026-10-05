package scoring

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"
)

type Context struct {
	MinPoints    int
	MaxPoints    int
	Solves       int
	MaxSolves    int
	EventStart   time.Time
	EventEnd     time.Time
	FirstSolveAt *time.Time
}

type Definition struct {
	Provider string             `json:"provider"`
	Revision int                `json:"revision"`
	Options  map[string]float64 `json:"options,omitempty"`
}

type Descriptor struct {
	Provider string   `json:"provider"`
	Revision int      `json:"revision"`
	Label    string   `json:"label"`
	Fields   []string `json:"fields"`
}

type Provider interface {
	Calculate(Context) (int, error)
}

type Factory func(map[string]float64) (Provider, error)

type registration struct {
	descriptor Descriptor
	factory    Factory
}

type Registry struct {
	mu        sync.RWMutex
	providers map[string]registration
}

type validatedProvider struct {
	provider Provider
}

func (provider validatedProvider) Calculate(context Context) (int, error) {
	if err := validateContext(context); err != nil {
		return 0, err
	}
	return provider.provider.Calculate(context)
}

func NewRegistry() *Registry {
	return &Registry{providers: map[string]registration{}}
}

func Builtins() *Registry {
	r := NewRegistry()
	r.MustRegister(Descriptor{Provider: "static", Revision: 1, Label: "Static", Fields: []string{"min_points", "max_points"}}, newStatic)
	r.MustRegister(Descriptor{Provider: "decay", Revision: 1, Label: "Balanced decay", Fields: []string{"min_points", "max_points", "solves"}}, newClassic)
	r.MustRegister(Descriptor{Provider: "logarithmic", Revision: 1, Label: "Gentle decay", Fields: []string{"min_points", "max_points", "solves"}}, newLogarithmic)
	r.MustRegister(Descriptor{Provider: "steep", Revision: 1, Label: "Steep decay", Fields: []string{"min_points", "max_points", "solves"}}, newSteep)
	r.MustRegister(Descriptor{Provider: "first-solve-time", Revision: 1, Label: "First solve time", Fields: []string{"min_points", "max_points", "event_start", "event_end", "first_solve_at"}}, newFirstSolveTime)
	r.MustRegister(Descriptor{Provider: "solve-threshold", Revision: 1, Label: "Solve threshold", Fields: []string{"max_points", "solves"}}, newSolveThreshold)
	r.MustRegister(Descriptor{Provider: "field-relative", Revision: 1, Label: "Field relative", Fields: []string{"min_points", "max_points", "solves", "max_solves"}}, newFieldRelative)
	return r
}

func (r *Registry) Register(descriptor Descriptor, factory Factory) error {
	if r == nil {
		return errors.New("registry is nil")
	}
	descriptor.Provider = normalizeProvider(descriptor.Provider)
	if descriptor.Provider == "" {
		return errors.New("provider is required")
	}
	if descriptor.Revision < 1 {
		return errors.New("revision must be positive")
	}
	if descriptor.Label == "" {
		return errors.New("label is required")
	}
	if factory == nil {
		return errors.New("factory is required")
	}
	key := providerKey(descriptor.Provider, descriptor.Revision)
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.providers == nil {
		r.providers = make(map[string]registration)
	}
	if _, exists := r.providers[key]; exists {
		return fmt.Errorf("provider %s is already registered", key)
	}
	descriptor.Fields = append([]string(nil), descriptor.Fields...)
	r.providers[key] = registration{descriptor: descriptor, factory: factory}
	return nil
}

func (r *Registry) MustRegister(descriptor Descriptor, factory Factory) {
	if err := r.Register(descriptor, factory); err != nil {
		panic(err)
	}
}

func (r *Registry) Compile(definition Definition) (Provider, error) {
	if r == nil {
		return nil, errors.New("registry is nil")
	}
	provider := normalizeProvider(definition.Provider)
	r.mu.RLock()
	registration, ok := r.providers[providerKey(provider, definition.Revision)]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("unknown scoring provider %s@%d", provider, definition.Revision)
	}
	options := make(map[string]float64, len(definition.Options))
	for key, value := range definition.Options {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return nil, fmt.Errorf("option %s must be finite", key)
		}
		options[key] = value
	}
	compiled, err := registration.factory(options)
	if err != nil {
		return nil, fmt.Errorf("compile %s@%d: %w", provider, definition.Revision, err)
	}
	return validatedProvider{provider: compiled}, nil
}

func (r *Registry) Fingerprint(definition Definition) (string, error) {
	if _, err := r.Compile(definition); err != nil {
		return "", err
	}
	options := definition.Options
	if options == nil {
		options = map[string]float64{}
	}
	payload, err := json.Marshal(struct {
		Provider string             `json:"provider"`
		Revision int                `json:"revision"`
		Options  map[string]float64 `json:"options"`
	}{Provider: normalizeProvider(definition.Provider), Revision: definition.Revision, Options: options})
	if err != nil {
		return "", fmt.Errorf("encode scoring definition: %w", err)
	}
	sum := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func (r *Registry) List() []Descriptor {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	list := make([]Descriptor, 0, len(r.providers))
	for _, registration := range r.providers {
		descriptor := registration.descriptor
		descriptor.Fields = append([]string(nil), descriptor.Fields...)
		list = append(list, descriptor)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].Provider == list[j].Provider {
			return list[i].Revision < list[j].Revision
		}
		return list[i].Provider < list[j].Provider
	})
	return list
}

func Calculate(registry *Registry, definition Definition, context Context) (int, error) {
	provider, err := registry.Compile(definition)
	if err != nil {
		return 0, err
	}
	return provider.Calculate(context)
}

func validateContext(context Context) error {
	if context.MinPoints < 0 {
		return errors.New("min points cannot be negative")
	}
	if context.MaxPoints < context.MinPoints {
		return errors.New("max points cannot be lower than min points")
	}
	if context.Solves < 0 {
		return errors.New("solves cannot be negative")
	}
	if context.MaxSolves < 0 {
		return errors.New("max solves cannot be negative")
	}
	return nil
}

func normalizeProvider(provider string) string {
	return strings.TrimSpace(provider)
}

func providerKey(provider string, revision int) string {
	return fmt.Sprintf("%s@%d", provider, revision)
}

func requireNoOptions(options map[string]float64) error {
	if len(options) != 0 {
		keys := make([]string, 0, len(options))
		for key := range options {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		return fmt.Errorf("unsupported options: %v", keys)
	}
	return nil
}

func clampScore(score, minimum, maximum float64) int {
	return int(math.Round(math.Max(minimum, math.Min(maximum, score))))
}
