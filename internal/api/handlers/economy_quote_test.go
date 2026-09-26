package handlers

import (
	"math"
	"testing"

	"github.com/anvil-lab/anvil/internal/config"
)

func TestConversionQuoteMatchesBlockDecay(t *testing.T) {
	cfg := config.EconomyConfig{
		P2CBlock: 50, P2CBase: 1, P2CRateDecay: 0.7, P2CMinRate: 0.25,
	}
	tests := []struct {
		name   string
		points float64
		blocks int
		want   float64
	}{
		{name: "first block", points: 50, want: 50},
		{name: "later block", points: 10, blocks: 1, want: 7},
		{name: "crosses blocks", points: 75, want: 67.5},
		{name: "rate floor", points: 50, blocks: 20, want: 12.5},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := conversionQuote(test.points, test.blocks, cfg); math.Abs(got-test.want) > 0.000001 {
				t.Fatalf("conversionQuote() = %v, want %v", got, test.want)
			}
		})
	}
}
