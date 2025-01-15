package models

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
	WeaponStats     map[string]*WeaponStats
}

type WeaponStats struct {
	Kills     int
	Shots     int
	Hits      int
	Headshots int
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
