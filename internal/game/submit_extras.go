package game

const (
	// SubmitNotLive is returned when the game is not in a scoreable state:
	// not started, paused, or stopped.
	SubmitNotLive SubmitOutcome = "not_live"
)
