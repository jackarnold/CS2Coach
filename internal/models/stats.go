package models

import "fmt"

type PlayerStats struct {
	Name            string
	SteamID         uint64
	Kills           int
	Deaths          int
	Assists         int
	Headshots       int
	ShotsTotal      int
	HitsTotal       int
	FlashAssists    int
	UtilityDamage   int
	OpeningDuels    int
	OpeningDuelsWon int
	ClutchAttempts  int
	ClutchesWon     int
	IsAlive         bool
	Team            int

	// Enhanced stats
	WeaponStats     map[string]*WeaponStats
	TradeKills      int
	TimesTraded     int
	RoundsSurvived  int
	SurvivalByPhase map[string]int // early, mid, late, clutch
	MapAreaKills    map[string]int
	MapAreaDeaths   map[string]int
}

func (ps *PlayerStats) Debug() string {
	return fmt.Sprintf("Name: %s, SteamID: %d, K/D/A: %d/%d/%d",
		ps.Name, ps.SteamID, ps.Kills, ps.Deaths, ps.Assists)
}

func (ps *PlayerStats) GetOrCreateWeaponStats(weapon string) *WeaponStats {
	if ps.WeaponStats == nil {
		ps.WeaponStats = make(map[string]*WeaponStats)
	}
	if ps.WeaponStats[weapon] == nil {
		ps.WeaponStats[weapon] = &WeaponStats{}
	}
	return ps.WeaponStats[weapon]
}

type WeaponStats struct {
	Kills     int
	Deaths    int
	Shots     int
	Hits      int
	Headshots int
	Damage    int
}

type AnalyzedStats struct {
	BasicStats    PlayerStats
	AdvancedStats map[string]float64
}

func NewAnalyzedStats(basic PlayerStats) *AnalyzedStats {
	return &AnalyzedStats{
		BasicStats:    basic,
		AdvancedStats: make(map[string]float64),
	}
}
