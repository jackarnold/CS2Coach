package ml

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"

	"github.com/richardkiene/CS2Coach/internal/analyzer"
	"github.com/richardkiene/CS2Coach/internal/models"
)

type ModelConfig struct {
	Features      []string  `json:"features"`
	Weights       []float64 `json:"weights"`
	LearningRate  float64   `json:"learning_rate"`
	ModelPath     string    `json:"model_path"`
	TrainingEpoch int       `json:"training_epoch"`
}

type Model struct {
	config    ModelConfig
	analyzer  *analyzer.Analyzer
	features  []string
	weights   []float64
	trainData []TrainingInstance
}

type TrainingInstance struct {
	Features []float64
	Label    float64
}

func NewModel(configPath string) (*Model, error) {
	config := ModelConfig{
		Features: []string{
			"KillsPerRound",
			"HeadshotPercentage",
			"Accuracy",
			"OpeningDuelSuccess",
			"ClutchSuccess",
			"UtilityDamagePerRound",
			"TradeEfficiency",
			"SurvivalRate",
		},
		LearningRate:  0.01,
		ModelPath:     "models/cs2coach.model",
		TrainingEpoch: 100,
	}

	if configPath != "" {
		if err := loadConfig(configPath, &config); err != nil {
			return nil, err
		}
	}

	return &Model{
		config:   config,
		analyzer: analyzer.NewAnalyzer(),
		features: config.Features,
		weights:  make([]float64, len(config.Features)),
	}, nil
}

func (m *Model) Train(matches []*models.Match) error {
	m.trainData = make([]TrainingInstance, 0)

	for _, match := range matches {
		for _, stats := range match.PlayerStats {
			analyzed := m.analyzer.AnalyzeMatch(match, stats.Name, fmt.Sprintf("%d", stats.SteamID), false)
			if analyzed == nil {
				continue
			}

			features := make([]float64, len(m.features))
			for i, feature := range m.features {
				features[i] = analyzed.AdvancedStats[feature]
			}

			impactScore := analyzer.CalculateImpactScore(&analyzed.BasicStats)
			m.trainData = append(m.trainData, TrainingInstance{
				Features: features,
				Label:    impactScore,
			})
		}
	}

	return m.trainModel()
}

func (m *Model) Predict(stats *models.AnalyzedStats) (*models.Prediction, error) {
	features := make([]float64, len(m.features))
	for i, feature := range m.features {
		features[i] = stats.AdvancedStats[feature]
	}

	prediction := m.predict(features)
	outcome := determineOutcome(prediction)

	return &models.Prediction{
		Probability: prediction,
		Outcome:     outcome,
	}, nil
}

func (m *Model) trainModel() error {
	for epoch := 0; epoch < m.config.TrainingEpoch; epoch++ {
		totalError := 0.0
		for _, instance := range m.trainData {
			predicted := m.predict(instance.Features)
			error := instance.Label - predicted

			for i := range m.weights {
				m.weights[i] += m.config.LearningRate * error * instance.Features[i]
			}
			totalError += math.Abs(error)
		}

		if totalError/float64(len(m.trainData)) < 0.01 {
			break
		}
	}

	return m.saveModel()
}

func (m *Model) predict(features []float64) float64 {
	sum := 0.0
	for i := range features {
		sum += features[i] * m.weights[i]
	}
	return sigmoid(sum)
}

func sigmoid(x float64) float64 {
	return 1.0 / (1.0 + math.Exp(-x))
}

func determineOutcome(probability float64) string {
	if probability >= 0.7 {
		return "High Impact"
	} else if probability >= 0.4 {
		return "Medium Impact"
	}
	return "Low Impact"
}

func loadConfig(path string, config *ModelConfig) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("error reading config: %v", err)
	}
	return json.Unmarshal(data, config)
}

func (m *Model) saveModel() error {
	modelData := struct {
		Features []string  `json:"features"`
		Weights  []float64 `json:"weights"`
	}{
		Features: m.features,
		Weights:  m.weights,
	}

	data, err := json.Marshal(modelData)
	if err != nil {
		return fmt.Errorf("error marshaling model: %v", err)
	}

	dir := filepath.Dir(m.config.ModelPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("error creating model directory: %v", err)
	}

	return os.WriteFile(m.config.ModelPath, data, 0644)
}

// Storage interface and implementations can be added here if needed
type Storage interface {
	SaveTrainingData(data []TrainingInstance) error
	LoadTrainingData() ([]TrainingInstance, error)
}
