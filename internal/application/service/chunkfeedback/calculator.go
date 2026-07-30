package chunkfeedback

const (
	StatusNormal  = "normal"
	StatusPending = "pending_optimization"
)

type Config struct {
	HighQualityThreshold float64
	LowQualityThreshold  float64
	WeightBoostFactor    float64
	WeightPenaltyFactor  float64
	AutoMarkThreshold    float64
	AutoMarkMinFeedbacks int
	MinWeight            float64
	MaxWeight            float64
}

type State struct {
	LikeCount     int
	DislikeCount  int
	PositiveRate  float64
	RecallWeight  float64
	QualityStatus string
}

type VoteChange struct {
	WasCreated bool
	IsChanged  bool
	IsPositive bool
}

func ApplyVote(state State, change VoteChange, config Config) State {
	switch {
	case change.WasCreated && change.IsPositive:
		state.LikeCount++
	case change.WasCreated:
		state.DislikeCount++
	case change.IsChanged && change.IsPositive:
		state.LikeCount++
		state.DislikeCount = decrementIfPositive(state.DislikeCount)
	case change.IsChanged:
		state.LikeCount = decrementIfPositive(state.LikeCount)
		state.DislikeCount++
	}
	return Recalculate(state, config)
}

func CancelVote(state State, wasPositive bool, config Config) State {
	if wasPositive {
		state.LikeCount = decrementIfPositive(state.LikeCount)
	} else {
		state.DislikeCount = decrementIfPositive(state.DislikeCount)
	}
	return Recalculate(state, config)
}

func Recalculate(state State, config Config) State {
	total := state.LikeCount + state.DislikeCount
	if total > 0 {
		state.PositiveRate = float64(state.LikeCount) / float64(total)
	} else {
		state.PositiveRate = 0
	}
	state.RecallWeight = RecallWeight(state.PositiveRate, total, config)
	state.QualityStatus = QualityStatus(state.QualityStatus, state.PositiveRate, total, config)
	return state
}

func RecallWeight(positiveRate float64, total int, config Config) float64 {
	if total == 0 {
		return 1.0
	}
	weight := 1.0
	if positiveRate >= config.HighQualityThreshold {
		weight = config.WeightBoostFactor
	} else if positiveRate < config.LowQualityThreshold {
		weight = config.WeightPenaltyFactor
	}
	return clampWeight(weight, config)
}

func QualityStatus(current string, positiveRate float64, total int, config Config) string {
	if positiveRate <= config.AutoMarkThreshold && total >= config.AutoMarkMinFeedbacks {
		return StatusPending
	}
	if current == StatusPending {
		return StatusNormal
	}
	return current
}

func decrementIfPositive(count int) int {
	if count <= 0 {
		return 0
	}
	return count - 1
}

func clampWeight(weight float64, config Config) float64 {
	if config.MinWeight > 0 && weight < config.MinWeight {
		return config.MinWeight
	}
	if config.MaxWeight > 0 && weight > config.MaxWeight {
		return config.MaxWeight
	}
	return weight
}
