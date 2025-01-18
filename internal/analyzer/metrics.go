package analyzer

import "github.com/richardkiene/CS2Coach/internal/models"

// CalculateAimScore returns detailed aim metrics (0-100)
func CalculateAimScore(stats *models.PlayerStats) map[string]float64 {
	metrics := make(map[string]float64)

	// First bullet accuracy
	if stats.FirstBulletShots > 0 {
		metrics["FirstBulletAccuracy"] = float64(stats.FirstBulletHits) / float64(stats.FirstBulletShots) * 100
	}

	// Spray control
	metrics["SprayControl"] = float64(stats.SprayTransfers) / float64(stats.Kills) * 100

	// Average reaction time (ms)
	if stats.ReactionTimeCount > 0 {
		metrics["AverageReactionTime"] = stats.ReactionTimeTotal / float64(stats.ReactionTimeCount)
	}

	return metrics
}

// CalculatePositioningScore returns positioning metrics (0-100)
func CalculatePositioningScore(stats *models.PlayerStats) map[string]float64 {
	metrics := make(map[string]float64)

	// Peek effectiveness
	if (stats.PeekKills + stats.PeekDeaths) > 0 {
		metrics["PeekEffectiveness"] = float64(stats.PeekKills) / float64(stats.PeekKills+stats.PeekDeaths) * 100
	}

	// Trade potential
	metrics["TradePotential"] = float64(stats.TradeKills) / float64(stats.Deaths) * 100

	// Site anchor performance
	metrics["SiteAnchor"] = float64(stats.SiteHolds) / float64(stats.RoundsSurvived) * 100

	return metrics
}

// CalculateUtilityScore returns utility usage metrics (0-100)
func CalculateUtilityScore(stats *models.PlayerStats) map[string]float64 {
	metrics := make(map[string]float64)

	// Flash effectiveness
	if stats.FlashesThrown > 0 {
		metrics["FlashEffectiveness"] = float64(stats.EnemiesFlashed) / float64(stats.FlashesThrown)
	}

	// Utility damage per round
	metrics["UtilityDamagePerRound"] = float64(stats.UtilityDamage) / float64(stats.RoundsSurvived)

	return metrics
}

// CalculateDecisionScore returns decision making metrics (0-100)
func CalculateDecisionScore(stats *models.PlayerStats) map[string]float64 {
	metrics := make(map[string]float64)

	// Entry success
	if stats.EntryAttempts > 0 {
		metrics["EntrySuccess"] = float64(stats.EntryKills) / float64(stats.EntryAttempts) * 100
	}

	// Clutch performance
	if stats.ClutchAttempts > 0 {
		metrics["ClutchSuccess"] = float64(stats.ClutchesWon) / float64(stats.ClutchAttempts) * 100
	}

	// Force buy impact
	if (stats.ForceBuyKills + stats.ForceBuyDeaths) > 0 {
		metrics["ForceBuyImpact"] = float64(stats.ForceBuyKills) / float64(stats.ForceBuyKills+stats.ForceBuyDeaths) * 100
	}

	return metrics
}

// CalculateLeetifyScore combines all metrics into a final score (0-100)
func CalculateImpactScore(stats *models.PlayerStats) float64 {
	aimMetrics := CalculateAimScore(stats)
	positioningMetrics := CalculatePositioningScore(stats)
	utilityMetrics := CalculateUtilityScore(stats)
	decisionMetrics := CalculateDecisionScore(stats)

	// Weight each category
	aimScore := averageMapValues(aimMetrics) * 0.35
	positioningScore := averageMapValues(positioningMetrics) * 0.25
	utilityScore := averageMapValues(utilityMetrics) * 0.20
	decisionScore := averageMapValues(decisionMetrics) * 0.20

	return aimScore + positioningScore + utilityScore + decisionScore
}

func averageMapValues(m map[string]float64) float64 {
	if len(m) == 0 {
		return 0
	}

	sum := 0.0
	for _, v := range m {
		sum += v
	}
	return sum / float64(len(m))
}
