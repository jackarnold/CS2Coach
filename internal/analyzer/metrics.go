package analyzer

import "github.com/richardkiene/CS2Coach/internal/models"

// CalculateImpactScore returns a player's impact score (0-100)
func CalculateImpactScore(stats *models.PlayerStats, roundCount int) float64 {
	kpr := float64(stats.Kills) / float64(roundCount)
	openingSuccess := float64(stats.OpeningDuelsWon) / float64(stats.OpeningDuels)
	clutchSuccess := float64(stats.ClutchesWon) / float64(stats.ClutchAttempts)

	return (kpr*0.4 + openingSuccess*0.3 + clutchSuccess*0.3) * 100
}

// CalculateUtilityScore returns a player's utility usage score (0-100)
func CalculateUtilityScore(stats *models.PlayerStats, roundCount int) float64 {
	utilityPerRound := float64(stats.UtilityDamage) / float64(roundCount)
	flashAssistsPerRound := float64(stats.FlashAssists) / float64(roundCount)

	return (utilityPerRound/30.0*0.6 + flashAssistsPerRound/0.5*0.4) * 100
}

// CalculateAimScore returns a player's aim score (0-100)
func CalculateAimScore(stats *models.PlayerStats) float64 {
	accuracy := float64(stats.HitsTotal) / float64(stats.ShotsTotal)
	headshotRate := float64(stats.Headshots) / float64(stats.Kills)

	return (accuracy*0.6 + headshotRate*0.4) * 100
}
