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
	OpeningDuels    int
	OpeningDuelsWon int
	IsAlive         bool
	Team            int

	// Enhanced stats
	TimesTraded     int
	RoundsSurvived  int
	SurvivalByPhase map[string]int // early, mid, late, clutch
	MapAreaKills    map[string]int
	MapAreaDeaths   map[string]int

	// Aim metrics
	FirstBulletHits   int
	FirstBulletShots  int
	FlickKills        int
	SprayTransfers    int
	PreFireKills      int
	ReactionTimeTotal float64
	ReactionTimeCount int

	// Positioning metrics
	CounterStrafeKills int
	PeekKills          int
	PeekDeaths         int
	FlashAssists       int
	SiteHolds          int
	RotationKills      int

	// Utility metrics
	FlashesThrown  int
	EnemiesFlashed int
	SmokesThrown   int
	MolotovDamage  int
	UtilityDamage  int

	// Decision making
	ForceBuyKills  int
	ForceBuyDeaths int
	ClutchAttempts int
	ClutchesWon    int
	EntryAttempts  int
	EntryKills     int
	TradeKills     int
	TradeDeaths    int
	RetakeKills    int

	// Economic tracking
	MoneySpent     int
	EquipmentValue int

	// Enhanced weapon stats
	WeaponStats   map[string]*WeaponStats
	SprayPatterns map[string][]Point

	// Leetify metrics
	EnemySpottedShots int
	EnemySpottedHits  int

	CounterStrafedShots  int
	SprayShots           int
	SprayHits            int
	CrosshairAdjustments []float64
	TotalDamage          int
	TimeToFirstDamage    []float64
	TradedDeaths         int
	UtilityStats         UtilityStats
}

type UtilityStats struct {
	HEGrenadesThrown   int
	HEDamage           int
	FlashesThrown      int
	MolotovsThrown     int
	SmokesThrown       int
	EnemiesFlashed     int
	TeammatesFlashed   int
	FlashAssists       int
	TotalBlindDuration float64
}

type LeetifyMetrics struct {
	LeetifyRating         float64
	AccuracyEnemySpotted  float64
	HeadshotAccuracy      float64
	CounterStrafing       float64
	SprayAccuracy         float64
	CrosshairPlacement    float64
	ADR                   float64
	TimeToFirstDamage     float64
	SpottedAccuracy       float64
	TradeKillPercentage   float64
	TradedDeathPercentage float64
	UtilityMetrics        UtilityMetrics
}

type UtilityMetrics struct {
	HEPerGame                 float64
	HEDamagePerGame           float64
	FlashesPerGame            float64
	MolotovsPerGame           float64
	SmokesPerGame             float64
	EnemiesFlashedPerGame     float64
	FlashAssistsPerGame       float64
	AvgBlindDuration          float64
	TeammatesFlashedPerGame   float64
	TotalBlindDurationPerGame float64
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

type Point struct {
	X, Y, Z float32
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
