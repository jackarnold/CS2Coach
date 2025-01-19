package analyzer

import "github.com/richardkiene/CS2Coach/internal/models"

func CalculateLeetifyMetrics(stats *models.PlayerStats, roundCount int) *models.LeetifyMetrics {
	metrics := &models.LeetifyMetrics{}

	// Accuracy metrics
	if stats.ShotsTotal > 0 {
		metrics.AccuracyEnemySpotted = float64(stats.HitsTotal) / float64(stats.ShotsTotal) * 100
		metrics.CounterStrafing = float64(stats.CounterStrafedShots) / float64(stats.ShotsTotal) * 100
	}

	if stats.Kills > 0 {
		metrics.HeadshotAccuracy = float64(stats.Headshots) / float64(stats.Kills) * 100
	}

	if stats.ShotsTotal > 0 {
		metrics.CounterStrafing = float64(stats.CounterStrafedShots) / float64(stats.ShotsTotal) * 100
	}

	if stats.SprayShots > 0 {
		metrics.SprayAccuracy = float64(stats.SprayHits) / float64(stats.SprayShots) * 100
	}

	// Average crosshair placement
	if len(stats.CrosshairAdjustments) > 0 {
		sum := 0.0
		for _, adj := range stats.CrosshairAdjustments {
			sum += adj
		}
		metrics.CrosshairPlacement = sum / float64(len(stats.CrosshairAdjustments))
	}

	// ADR calculation
	if roundCount > 0 {
		metrics.ADR = float64(stats.TotalDamage) / float64(roundCount)
	}

	// Time to Damage
	if len(stats.TimeToFirstDamage) > 0 {
		sum := 0.0
		for _, time := range stats.TimeToFirstDamage {
			sum += time
		}
		metrics.TimeToFirstDamage = sum / float64(len(stats.TimeToFirstDamage))
	}

	// Trade metrics
	if stats.Kills > 0 {
		metrics.TradeKillPercentage = float64(stats.TradeKills) / float64(stats.Kills) * 100
	}

	if stats.Deaths > 0 {
		metrics.TradedDeathPercentage = float64(stats.TradedDeaths) / float64(stats.Deaths) * 100
	}

	// Utility metrics per game
	metrics.UtilityMetrics = CalculateUtilityMetrics(&stats.UtilityStats, float64(roundCount)/30.0)

	// Calculate overall Leetify Rating
	metrics.LeetifyRating = calculateLeetifyRating(stats, metrics)

	return metrics
}

func CalculateUtilityMetrics(stats *models.UtilityStats, games float64) models.UtilityMetrics {
	return models.UtilityMetrics{
		HEPerGame:                 float64(stats.HEGrenadesThrown) / games,
		HEDamagePerGame:           float64(stats.HEDamage) / games,
		FlashesPerGame:            float64(stats.FlashesThrown) / games,
		MolotovsPerGame:           float64(stats.MolotovsThrown) / games,
		SmokesPerGame:             float64(stats.SmokesThrown) / games,
		EnemiesFlashedPerGame:     float64(stats.EnemiesFlashed) / games,
		TeammatesFlashedPerGame:   float64(stats.TeammatesFlashed) / games,
		FlashAssistsPerGame:       float64(stats.FlashAssists) / games,
		AvgBlindDuration:          stats.TotalBlindDuration / float64(stats.EnemiesFlashed),
		TotalBlindDurationPerGame: stats.TotalBlindDuration / games,
	}
}

func calculateLeetifyRating(stats *models.PlayerStats, metrics *models.LeetifyMetrics) float64 {
	// Base rating from KDA
	rating := (float64(stats.Kills)*1.0 + float64(stats.Assists)*0.7) / float64(stats.Deaths+1) * 100

	// Impact multipliers
	rating *= (1 + metrics.ADR/300.0)                   // Damage impact
	rating *= (1 + metrics.HeadshotAccuracy/200.0)      // Aim impact
	rating *= (1 + float64(stats.FlashAssists)/50.0)    // Utility impact
	rating *= (1 + metrics.TradedDeathPercentage/200.0) // Trading impact

	return rating
}

// CalculateAimScore returns detailed aim metrics (0-100)
func CalculateAimScore(stats *models.PlayerStats) map[string]float64 {
	metrics := make(map[string]float64)

	// First bullet accuracy
	if stats.FirstBulletShots > 0 {
		metrics["FirstBulletAccuracy"] = float64(stats.FirstBulletHits) / float64(stats.FirstBulletShots) * 100
	}

	// Spray control
	if stats.Kills > 0 {
		metrics["SprayControl"] = float64(stats.SprayTransfers) / float64(stats.Kills) * 100
	}

	// Average reaction time (ms)
	if stats.ReactionTimeCount > 0 {
		metrics["AverageReactionTime"] = stats.ReactionTimeTotal / float64(stats.ReactionTimeCount)
	}

	// Headshot percentage
	if stats.Kills > 0 {
		metrics["HeadshotPercentage"] = float64(stats.Headshots) / float64(stats.Kills) * 100
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
