package models

type Prediction struct {
	Probability float64 // Probability value between 0 and 1
	Outcome     string  // Outcome (e.g., "Win", "Loss")
}

func NewPrediction(probability float64, outcome string) *Prediction {
	return &Prediction{Probability: probability, Outcome: outcome}
}
