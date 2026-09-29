package config

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
)

//go:embed presets/h7-ledger-v1.json
var defaultEconomyPresetJSON []byte

type economyPreset struct {
	ID              string        `json:"id"`
	Version         string        `json:"version"`
	Name            string        `json:"name"`
	DifficultyOrder []string      `json:"difficulty_order"`
	Parameters      EconomyConfig `json:"parameters"`
}

// EconomyPolicyDescriptor identifies the named baseline and fingerprints the
// effective parameters after config-file and environment overrides.
type EconomyPolicyDescriptor struct {
	ID                string `json:"id"`
	Version           string `json:"version"`
	Name              string `json:"name"`
	Checksum          string `json:"checksum"`
	CanonicalChecksum string `json:"canonical_checksum"`
	Customized        bool   `json:"customized"`
}

type EconomyPolicyDocument struct {
	SchemaVersion   int           `json:"schema_version"`
	ID              string        `json:"id"`
	Version         string        `json:"version"`
	Name            string        `json:"name"`
	DifficultyOrder []string      `json:"difficulty_order"`
	Parameters      EconomyConfig `json:"parameters"`
	Checksum        string        `json:"checksum"`
	Customized      bool          `json:"customized"`
}

var builtInEconomyPreset = mustLoadEconomyPreset(defaultEconomyPresetJSON)

func mustLoadEconomyPreset(data []byte) economyPreset {
	var preset economyPreset
	if err := json.Unmarshal(data, &preset); err != nil {
		panic("invalid embedded economy preset: " + err.Error())
	}
	preset.Parameters.PresetID = preset.ID
	preset.Parameters.PresetVersion = preset.Version
	preset.Parameters.PresetName = preset.Name
	return preset
}

func cloneEconomyConfig(source EconomyConfig) EconomyConfig {
	clone := source
	clone.Ceilings = append([]float64(nil), source.Ceilings...)
	clone.LaunchCosts = append([]float64(nil), source.LaunchCosts...)
	clone.CrowdFloors = append([]float64(nil), source.CrowdFloors...)
	clone.CrowdHalflives = append([]float64(nil), source.CrowdHalflives...)
	clone.ExtCostFracs = append([]float64(nil), source.ExtCostFracs...)
	clone.TimerSteps = append([]float64(nil), source.TimerSteps...)
	return clone
}

func economyPolicyChecksum(policy EconomyConfig, difficultyOrder []string) string {
	// Metadata names the baseline but is not itself an economic rule. Exclude it
	// so the checksum changes exactly when effective behavior changes.
	policy.PresetID = ""
	policy.PresetVersion = ""
	policy.PresetName = ""
	encoded, err := json.Marshal(struct {
		DifficultyOrder []string      `json:"difficulty_order"`
		Parameters      EconomyConfig `json:"parameters"`
	}{DifficultyOrder: difficultyOrder, Parameters: policy})
	if err != nil {
		return "unavailable"
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func (c Config) EconomyPolicyDescriptor() EconomyPolicyDescriptor {
	canonical := builtInEconomyPreset.Parameters
	activeChecksum := economyPolicyChecksum(c.Economy, builtInEconomyPreset.DifficultyOrder)
	canonicalChecksum := economyPolicyChecksum(canonical, builtInEconomyPreset.DifficultyOrder)
	return EconomyPolicyDescriptor{
		ID:                c.Economy.PresetID,
		Version:           c.Economy.PresetVersion,
		Name:              c.Economy.PresetName,
		Checksum:          activeChecksum,
		CanonicalChecksum: canonicalChecksum,
		Customized:        activeChecksum != canonicalChecksum,
	}
}

func (c Config) EconomyPolicyDocument() EconomyPolicyDocument {
	descriptor := c.EconomyPolicyDescriptor()
	return EconomyPolicyDocument{
		SchemaVersion:   1,
		ID:              descriptor.ID,
		Version:         descriptor.Version,
		Name:            descriptor.Name,
		DifficultyOrder: append([]string(nil), builtInEconomyPreset.DifficultyOrder...),
		Parameters:      cloneEconomyConfig(c.Economy),
		Checksum:        descriptor.Checksum,
		Customized:      descriptor.Customized,
	}
}
