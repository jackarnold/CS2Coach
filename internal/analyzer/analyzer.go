package analyzer

import (
	"fmt"
	"strconv"

	"github.com/richardkiene/CS2Coach/internal/models"
)

type Analyzer struct{}

func NewAnalyzer() *Analyzer {
	return &Analyzer{}
}

func (a *Analyzer) AnalyzeMatch(match *models.Match, playerName string, steamID string) *models.AnalyzedStats {
	var targetStats *models.PlayerStats
	var steamIDUint uint64
	var err error

	if steamID != "" {
		steamIDUint, err = strconv.ParseUint(steamID, 10, 64)
		if err != nil {
			return nil
		}
	}

	// Find target player
	for _, stats := range match.PlayerStats {
		if (playerName != "" && stats.Name == playerName) ||
			(steamID != "" && stats.SteamID == steamIDUint) {
			targetStats = stats
			break
		}
	}

	fmt.Printf("Analyzing stats for %s...\n", playerName)
	if targetStats != nil {
		fmt.Printf("Found player stats: K/D/A: %d/%d/%d\n",
			targetStats.Kills, targetStats.Deaths, targetStats.Assists)
	}

	if targetStats == nil {
		return nil
	}

	analyzed := models.NewAnalyzedStats(*targetStats)

	// Calculate advanced metrics
	roundCount := len(match.Events)
	analyzed.AdvancedStats["KillsPerRound"] = float64(targetStats.Kills) / float64(roundCount)
	analyzed.AdvancedStats["HeadshotPercentage"] = float64(targetStats.Headshots) / float64(targetStats.Kills) * 100
	analyzed.AdvancedStats["Accuracy"] = float64(targetStats.HitsTotal) / float64(targetStats.ShotsTotal) * 100
	analyzed.AdvancedStats["OpeningDuelSuccess"] = float64(targetStats.OpeningDuelsWon) / float64(targetStats.OpeningDuels) * 100
	analyzed.AdvancedStats["ClutchSuccess"] = float64(targetStats.ClutchesWon) / float64(targetStats.ClutchAttempts) * 100
	analyzed.AdvancedStats["UtilityDamagePerRound"] = float64(targetStats.UtilityDamage) / float64(roundCount)

	return analyzed
}
