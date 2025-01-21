package analyzer

import (
	"fmt"

	"github.com/richardkiene/CS2Coach/internal/models"
)

func calculateHLTVRating(stats *models.PlayerStats, roundCount int) float64 {
	// HLTV 2.0 Rating calculation
	killRating := float64(stats.Kills) / float64(roundCount) / 0.679
	survivalRating := float64(roundCount-stats.Deaths) / float64(roundCount) / 0.317

	// Rounds with multiple kills
	multiKillRounds := stats.TwoKills + stats.ThreeKills + stats.FourKills + stats.FiveKills
	roundsWithMultipleKillsRating := float64(multiKillRounds) / float64(roundCount) / 1.277

	return (killRating + 0.7*survivalRating + roundsWithMultipleKillsRating) / 2.7
}

func CalculateLeetifyMetrics(stats *models.PlayerStats, roundCount int) *models.LeetifyMetrics {
	if roundCount == 0 {
		return &models.LeetifyMetrics{} // Return empty metrics if no real rounds played
	}

	metrics := &models.LeetifyMetrics{
		MultiKills: map[string]int{
			"two":   stats.TwoKills,
			"three": stats.ThreeKills,
			"four":  stats.FourKills,
			"five":  stats.FiveKills,
		},
	}
	games := float64(roundCount) / 30.0

	// ADR - Total damage divided by rounds
	fmt.Println("TotalDamage: " + fmt.Sprint(stats.TotalDamage))
	fmt.Println("roundCount: " + fmt.Sprint(roundCount))
	metrics.ADR = float64(stats.TotalDamage) / float64(roundCount)

	// Accuracy calculations
	if stats.ShotsTotal > 0 {
		// Overall accuracy
		metrics.AccuracyAll = float64(stats.HitsTotal) / float64(stats.ShotsTotal) * 100

		// Spotted accuracy - hits on spotted enemies / shots at spotted enemies
		if stats.EnemySpottedShots > 0 {
			metrics.SpottedAccuracy = float64(stats.EnemySpottedHits) / float64(stats.EnemySpottedShots) * 100
		}
	}

	// Head accuracy and headshot percentage
	if stats.HitsTotal > 0 {
		metrics.HeadAccuracy = float64(stats.Headshots) / float64(stats.HitsTotal) * 100
	}
	if stats.Kills > 0 {
		metrics.HeadshotKillPercentage = float64(stats.Headshots) / float64(stats.Kills) * 100
	}

	// Spray control
	if stats.SprayShots > 0 {
		metrics.SprayAccuracy = float64(stats.SprayHits) / float64(stats.SprayShots) * 100
	}

	// Counter-strafing percentage
	if stats.ShotsTotal > 0 {
		metrics.CounterStrafing = float64(stats.CounterStrafedShots) / float64(stats.ShotsTotal) * 100
	}

	// Time to damage - Average time between spotting and hitting enemies
	if len(stats.TimeToFirstDamage) > 0 {
		sum := 0.0
		for _, time := range stats.TimeToFirstDamage {
			sum += time
		}
		metrics.TimeToFirstDamage = (sum / float64(len(stats.TimeToFirstDamage))) * 1000 // Convert to ms
	}

	// Trade metrics
	if stats.TradeKillOpportunities > 0 {
		metrics.TradeKillAttemptRate = float64(stats.TradeKillAttempts) / float64(stats.TradeKillOpportunities) * 100
	}
	if stats.TradeKillAttempts > 0 {
		metrics.TradeKillSuccessRate = float64(stats.TradeKills) / float64(stats.TradeKillAttempts) * 100
	}
	if stats.TradedDeathOpportunities > 0 {
		metrics.TradedDeathAttemptRate = float64(stats.TradedDeathAttempts) / float64(stats.TradedDeathOpportunities) * 100
	}
	if stats.TradedDeathAttempts > 0 {
		metrics.TradedDeathSuccessRate = float64(stats.TradedDeaths) / float64(stats.TradedDeathAttempts) * 100
	}

	// Calculate HLTV Rating
	killRating := float64(stats.Kills) / float64(roundCount) / 0.679
	survivalRating := float64(roundCount-stats.Deaths) / float64(roundCount) / 0.317
	multiKillRounds := stats.TwoKills + stats.ThreeKills + stats.FourKills + stats.FiveKills
	roundsWithMultiKills := float64(multiKillRounds) / float64(roundCount) / 1.277
	metrics.HLTV = (killRating + 0.7*survivalRating + roundsWithMultiKills) / 2.7

	// Utility metrics
	metrics.UtilityMetrics = CalculateUtilityMetrics(&stats.UtilityStats, games)
	metrics.AvgHEDamage = float64(stats.UtilityStats.HEDamage) / float64(roundCount)
	metrics.AvgTeamHEDamage = float64(stats.TeamUtilityDamage) / float64(roundCount)
	metrics.AvgUnusedUtilityValue = float64(stats.UnusedUtilityValue) / float64(roundCount)

	// Calculate final Leetify Rating using component scores
	aimScore := calculateAimSubScore(metrics)
	utilityScore := calculateUtilitySubScore(metrics, stats, roundCount)
	survivalScore := float64(stats.RoundsSurvived) / float64(roundCount)
	impactScore := calculateImpactSubScore(stats)

	metrics.LeetifyRating = calculateFinalRating(aimScore, utilityScore, survivalScore, impactScore)

	return metrics
}

func calculateAimSubScore(metrics *models.LeetifyMetrics) float64 {
	return (metrics.HeadshotKillPercentage/100*0.3 +
		metrics.AccuracyAll/100*0.3 +
		metrics.SpottedAccuracy/100*0.4)
}

func calculateUtilitySubScore(metrics *models.LeetifyMetrics, stats *models.PlayerStats, roundCount int) float64 {
	return (float64(stats.FlashAssists)/float64(roundCount)*0.4 +
		metrics.AvgHEDamage/30*0.3 +
		metrics.UtilityMetrics.EnemiesFlashedPerGame/3*0.3)
}

func calculateImpactSubScore(stats *models.PlayerStats) float64 {
	if stats.Deaths == 0 {
		return float64(stats.Kills)
	}
	return float64(stats.Kills) / float64(stats.Deaths)
}

func calculateFinalRating(aim, utility, survival, impact float64) float64 {
	weightedScore := aim*0.4 + utility*0.3 + survival*0.15 + impact*0.15
	return ((weightedScore - 0.5) * 20) - 10
}

func CalculateUtilityMetrics(stats *models.UtilityStats, games float64) models.UtilityMetrics {
	utilityMetrics := models.UtilityMetrics{
		HEPerGame:       float64(stats.HEGrenadesThrown) / games,
		HEDamagePerGame: float64(stats.HEDamage) / games,
		FlashesPerGame:  float64(stats.FlashesThrown) / games,
		MolotovsPerGame: float64(stats.MolotovsThrown) / games,
		SmokesPerGame:   float64(stats.SmokesThrown) / games,
	}

	// Flash metrics
	if stats.FlashesThrown > 0 {
		utilityMetrics.EnemiesFlashedPerGame = float64(stats.EnemiesFlashed) / games
		utilityMetrics.TeammatesFlashedPerGame = float64(stats.TeammatesFlashed) / games
		utilityMetrics.FlashAssistsPerGame = float64(stats.FlashAssists) / games
	}

	// Blind duration metrics
	if stats.EnemiesFlashed > 0 {
		utilityMetrics.AvgBlindDuration = stats.TotalBlindDuration / float64(stats.EnemiesFlashed)
		utilityMetrics.TotalBlindDurationPerGame = stats.TotalBlindDuration / games
	}

	return utilityMetrics
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
	if stats.UtilityStats.FlashesThrown > 0 {
		metrics["FlashEffectiveness"] = float64(stats.UtilityStats.EnemiesFlashed) / float64(stats.UtilityStats.FlashesThrown)
	}

	// Utility damage per round
	if stats.RoundsSurvived > 0 {
		metrics["UtilityDamagePerRound"] = float64(stats.UtilityStats.HEDamage) / float64(stats.RoundsSurvived)
	}

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
