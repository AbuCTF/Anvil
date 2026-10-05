package scoring

import (
	"errors"
	"math"
)

func ChallengeValue(scoreType string, maximum, minimum, decay, solves int) (int, error) {
	if maximum < 0 {
		return 0, errors.New("maximum points cannot be negative")
	}
	if minimum < 0 || minimum > maximum {
		return 0, errors.New("minimum points must be between zero and maximum points")
	}
	if solves < 0 {
		return 0, errors.New("solves cannot be negative")
	}
	if scoreType == "" || scoreType == "static" {
		return maximum, nil
	}
	if scoreType != "dynamic" {
		return 0, errors.New("score type must be static or dynamic")
	}
	if decay < 1 {
		return 0, errors.New("decay must be positive")
	}
	progress := math.Min(float64(solves)/float64(decay), 1)
	value := float64(maximum) - float64(maximum-minimum)*progress*progress
	return int(math.Ceil(value)), nil
}

func AllocatePoints(weights []int, total int) ([]int, error) {
	if total < 0 {
		return nil, errors.New("total points cannot be negative")
	}
	weightTotal := 0
	for _, weight := range weights {
		if weight < 0 {
			return nil, errors.New("flag weights cannot be negative")
		}
		weightTotal += weight
	}
	allocated := make([]int, len(weights))
	if weightTotal == 0 || total == 0 {
		return allocated, nil
	}
	cumulative := 0
	previous := 0
	for index, weight := range weights {
		cumulative += weight
		current := int(math.Round(float64(cumulative) * float64(total) / float64(weightTotal)))
		allocated[index] = current - previous
		previous = current
	}
	return allocated, nil
}
