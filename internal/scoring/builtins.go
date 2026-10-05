package scoring

import (
	"errors"
	"math"
)

type calculator func(Context) (int, error)

func (calculate calculator) Calculate(context Context) (int, error) {
	return calculate(context)
}

func newStatic(options map[string]float64) (Provider, error) {
	if err := requireNoOptions(options); err != nil {
		return nil, err
	}
	return calculator(func(context Context) (int, error) {
		return context.MaxPoints, nil
	}), nil
}

func newClassic(options map[string]float64) (Provider, error) {
	if err := requireNoOptions(options); err != nil {
		return nil, err
	}
	return calculator(func(context Context) (int, error) {
		x := math.Max(float64(context.Solves-1), 0) / 11.92201
		ratio := 1 / (1 + math.Pow(x, 1.206069))
		score := float64(context.MinPoints) + float64(context.MaxPoints-context.MinPoints)*ratio
		return clampScore(score, float64(context.MinPoints), float64(context.MaxPoints)), nil
	}), nil
}

func newLogarithmic(options map[string]float64) (Provider, error) {
	if err := requireNoOptions(options); err != nil {
		return nil, err
	}
	return calculator(func(context Context) (int, error) {
		const gradient = 10.0
		const decay = 60.0
		minimum := 1 + (gradient-1)/decay
		x := 1 + ((gradient-1)/decay)*float64(context.Solves)
		ratio := math.Log(x/minimum) / math.Log(gradient/minimum)
		score := math.Ceil(float64(context.MaxPoints) - float64(context.MaxPoints-context.MinPoints)*ratio)
		return int(math.Max(float64(context.MinPoints), math.Min(float64(context.MaxPoints), score))), nil
	}), nil
}

func newSteep(options map[string]float64) (Provider, error) {
	if err := requireNoOptions(options); err != nil {
		return nil, err
	}
	return calculator(func(context Context) (int, error) {
		ratio := 1 / (1 + math.Max(float64(context.Solves-1), 0)/6)
		score := float64(context.MinPoints) + float64(context.MaxPoints-context.MinPoints)*ratio
		return clampScore(score, float64(context.MinPoints), float64(context.MaxPoints)), nil
	}), nil
}

func newFirstSolveTime(options map[string]float64) (Provider, error) {
	maximumScoreTime := 0.8
	for key, value := range options {
		if key != "maximum_score_time" {
			return nil, errors.New("unsupported option: " + key)
		}
		maximumScoreTime = value
	}
	if maximumScoreTime <= 0 || maximumScoreTime > 1 {
		return nil, errors.New("maximum_score_time must be greater than 0 and at most 1")
	}
	return calculator(func(context Context) (int, error) {
		if context.FirstSolveAt == nil {
			return context.MaxPoints, nil
		}
		if context.EventStart.IsZero() || context.EventEnd.IsZero() || !context.EventEnd.After(context.EventStart) {
			return 0, errors.New("event start and end must define a positive duration")
		}
		eventLength := context.EventEnd.Sub(context.EventStart).Seconds()
		steps := context.MaxPoints - context.MinPoints + 1
		if context.MaxPoints == 0 || steps <= 0 {
			return context.MaxPoints, nil
		}
		tickRate := maximumScoreTime * eventLength / float64(steps)
		elapsed := context.FirstSolveAt.Sub(context.EventStart).Seconds()
		score := float64(context.MinPoints) + math.Trunc(elapsed/tickRate)
		return int(math.Max(float64(context.MinPoints), math.Min(float64(context.MaxPoints), score))), nil
	}), nil
}

func newSolveThreshold(options map[string]float64) (Provider, error) {
	threshold := 2.0
	for key, value := range options {
		if key != "awarded_solves" {
			return nil, errors.New("unsupported option: " + key)
		}
		threshold = value
	}
	if threshold < 0 || math.Trunc(threshold) != threshold {
		return nil, errors.New("awarded_solves must be a non-negative integer")
	}
	return calculator(func(context Context) (int, error) {
		if float64(context.Solves) > threshold {
			return 0, nil
		}
		return context.MaxPoints, nil
	}), nil
}

func newFieldRelative(options map[string]float64) (Provider, error) {
	if err := requireNoOptions(options); err != nil {
		return nil, err
	}
	const p0 = 0.7
	const p1 = 0.96
	c0 := -math.Atanh(p0)
	c1 := math.Atanh(p1)
	a := func(x float64) float64 { return (1 - math.Tanh(x)) / 2 }
	b := func(x float64) float64 {
		return (a((c1-c0)*x+c0) - a(c1)) / (a(c0) - a(c1))
	}
	return calculator(func(context Context) (int, error) {
		maximumSolves := math.Max(1, float64(context.MaxSolves))
		curve := func(solves float64) float64 {
			return float64(context.MinPoints) + float64(context.MaxPoints-context.MinPoints)*b(solves/maximumSolves)
		}
		score := math.Max(curve(float64(context.Solves)), curve(maximumSolves))
		return int(math.Round(score)), nil
	}), nil
}
