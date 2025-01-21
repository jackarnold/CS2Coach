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

func (a *Analyzer) AnalyzeMatch(match *models.Match, playerName string, steamID string, verbose bool) *models.AnalyzedStats {
	var targetStats *models.PlayerStats
	var steamIDUint uint64
	var err error

	if verbose {
		fmt.Printf("PlayerStats map contents:\n")
		for id, stats := range match.PlayerStats {
			fmt.Printf("ID: %d, Name: %s, K/D/A: %d/%d/%d\n",
				id, stats.Name, stats.Kills, stats.Deaths, stats.Assists)
		}
	}

	if steamID != "" {
		steamIDUint, err = strconv.ParseUint(steamID, 10, 64)
		if err != nil {
			fmt.Printf("Error parsing steamID: %v\n", err)
			return nil
		}
	}

	targetStats = match.GetOrCreatePlayerStats(steamIDUint, playerName)
	if targetStats == nil {
		fmt.Printf("Could not find or create stats for player: %s\n", playerName)
		return nil
	}

	fmt.Printf("Found target stats for %s: K/D/A: %d/%d/%d\n",
		playerName, targetStats.Kills, targetStats.Deaths, targetStats.Assists)

	analyzed := models.NewAnalyzedStats(*targetStats)
	roundCount := a.countRounds(match.Events)

	// Calculate base metrics
	analyzed.AdvancedStats["KillsPerRound"] = float64(targetStats.Kills) / float64(roundCount)
	analyzed.AdvancedStats["HeadshotPercentage"] = float64(targetStats.Headshots) / float64(targetStats.Kills) * 100
	analyzed.AdvancedStats["Accuracy"] = float64(targetStats.HitsTotal) / float64(targetStats.ShotsTotal) * 100
	analyzed.AdvancedStats["OpeningDuelSuccess"] = float64(targetStats.OpeningDuelsWon) / float64(targetStats.OpeningDuels) * 100
	analyzed.AdvancedStats["ClutchSuccess"] = float64(targetStats.ClutchesWon) / float64(targetStats.ClutchAttempts) * 100
	analyzed.AdvancedStats["UtilityDamagePerRound"] = float64(targetStats.UtilityStats.HEDamage) / float64(roundCount)

	// Calculate enhanced metrics
	analyzed.AdvancedStats["TradeEfficiency"] = float64(targetStats.TradeKills) / float64(targetStats.TimesTraded) * 100
	analyzed.AdvancedStats["SurvivalRate"] = float64(targetStats.RoundsSurvived) / float64(roundCount) * 100

	// Calculate weapon-specific metrics
	for weapon, stats := range targetStats.WeaponStats {
		if stats.Shots > 0 {
			weaponAccKey := fmt.Sprintf("%sAccuracy", weapon)
			analyzed.AdvancedStats[weaponAccKey] = float64(stats.Hits) / float64(stats.Shots) * 100

			weaponHSKey := fmt.Sprintf("%sHSRate", weapon)
			analyzed.AdvancedStats[weaponHSKey] = float64(stats.Headshots) / float64(stats.Kills) * 100
		}
	}

	return analyzed
}

func (a *Analyzer) countRounds(events []models.Event) int {
	roundCount := 0
	fmt.Println("\nCounting rounds:")
	for _, event := range events {
		fmt.Printf("Event type: %s\n", event.Type)
		if event.Type == "round_end" {
			roundCount++
		}
	}
	fmt.Printf("Total round count: %d\n", roundCount)
	return roundCount
}
