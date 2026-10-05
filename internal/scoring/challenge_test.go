package scoring

import "testing"

func TestChallengeValue(t *testing.T) {
	tests := []struct {
		name      string
		scoreType string
		maximum   int
		minimum   int
		decay     int
		solves    int
		want      int
	}{
		{name: "static", scoreType: "static", maximum: 500, minimum: 100, decay: 50, solves: 50, want: 500},
		{name: "dynamic initial", scoreType: "dynamic", maximum: 500, minimum: 100, decay: 20, solves: 0, want: 500},
		{name: "dynamic midpoint", scoreType: "dynamic", maximum: 500, minimum: 100, decay: 20, solves: 10, want: 400},
		{name: "dynamic floor", scoreType: "dynamic", maximum: 500, minimum: 100, decay: 20, solves: 20, want: 100},
		{name: "dynamic clamped", scoreType: "dynamic", maximum: 500, minimum: 100, decay: 20, solves: 200, want: 100},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ChallengeValue(test.scoreType, test.maximum, test.minimum, test.decay, test.solves)
			if err != nil || got != test.want {
				t.Fatalf("ChallengeValue() = %d, %v; want %d", got, err, test.want)
			}
		})
	}
}

func TestChallengeValueRejectsInvalidConfiguration(t *testing.T) {
	for _, input := range []struct {
		scoreType string
		maximum   int
		minimum   int
		decay     int
		solves    int
	}{
		{scoreType: "other", maximum: 500, minimum: 100, decay: 20},
		{scoreType: "dynamic", maximum: 500, minimum: 600, decay: 20},
		{scoreType: "dynamic", maximum: 500, minimum: 100, decay: 0},
		{scoreType: "dynamic", maximum: 500, minimum: 100, decay: 20, solves: -1},
	} {
		if _, err := ChallengeValue(input.scoreType, input.maximum, input.minimum, input.decay, input.solves); err == nil {
			t.Fatalf("ChallengeValue(%+v) succeeded", input)
		}
	}
}

func TestAllocatePoints(t *testing.T) {
	got, err := AllocatePoints([]int{200, 300}, 400)
	if err != nil || len(got) != 2 || got[0] != 160 || got[1] != 240 {
		t.Fatalf("AllocatePoints() = %v, %v; want [160 240]", got, err)
	}
	got, err = AllocatePoints([]int{1, 1, 1}, 2)
	if err != nil || got[0]+got[1]+got[2] != 2 {
		t.Fatalf("AllocatePoints() = %v, %v; want a total of 2", got, err)
	}
	if _, err := AllocatePoints([]int{1, -1}, 2); err == nil {
		t.Fatal("AllocatePoints() accepted a negative weight")
	}
}
