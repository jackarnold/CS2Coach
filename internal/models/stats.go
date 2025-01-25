package models

import "fmt"

type PlayerStats struct {
	Name       string
	SteamID    uint64
	Kills      int
	Deaths     int
	Assists    int
	Headshots  int
	ShotsTotal int
	HitsTotal  int
	IsAlive    bool
	Team       int

	// Aim metrics
	EnemySpottedShots   int
	EnemySpottedHits    int
	SprayShots          int
	SprayHits           int
	CounterStrafedShots int
	TimeToFirstDamage   []float64
	TimeToFirstShot     []float64
	FirstBulletHits     int
	FirstBulletShots    int
	CrosshairPlacement  []float64

	// Trade metrics
	TradeKills               int
	TradeKillOpportunities   int
	TradeKillAttempts        int
	TradedDeaths             int
	TradedDeathOpportunities int
	TradedDeathAttempts      int
	TimesTraded              int

	// Round metrics
	RoundsSurvived  int
	RoundsActive    int
	SurvivalByPhase map[string]int
	OpeningDuels    int
	OpeningDuelsWon int
	ClutchAttempts  int
	ClutchesWon     int

	// Multi-kill tracking
	TwoKills   int
	ThreeKills int
	FourKills  int
	FiveKills  int

	// Utility metrics
	UtilityStats       UtilityStats
	UnusedUtilityValue int
	TeamUtilityDamage  int
	TotalDamage        int

	// Movement tracking
	Velocity map[string]float64

	WeaponStats       map[string]*WeaponStats
	MapAreaKills      map[string]int
	MapAreaDeaths     map[string]int
	FlashAssists      int
	PeekKills         int
	PeekDeaths        int
	SprayTransfers    int
	ReactionTimeTotal float64
	ReactionTimeCount int
	EquipmentValue    int
	MoneySpent        int
	SiteHolds         int
	EntryKills        int
	EntryAttempts     int
	ForceBuyKills     int
	ForceBuyDeaths    int
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
	LeetifyRating float64
	HLTV          float64
	ADR           float64

	// Aim metrics
	AccuracyAll            float64
	SpottedAccuracy        float64
	HeadAccuracy           float64
	HeadshotKillPercentage float64
	SprayAccuracy          float64
	CounterStrafing        float64
	CrosshairPlacement     float64
	TimeToFirstDamage      float64

	// Utility metrics
	UtilityMetrics        UtilityMetrics
	AvgHEDamage           float64
	AvgTeamHEDamage       float64
	AvgUnusedUtilityValue float64

	// Trade metrics
	TradeKillAttemptRate   float64
	TradeKillSuccessRate   float64
	TradedDeathAttemptRate float64
	TradedDeathSuccessRate float64

	MultiKills map[string]int
}

type UtilityMetrics struct {
	HEPerGame                 float64
	HEDamagePerGame           float64
	FlashesPerGame            float64
	MolotovsPerGame           float64
	SmokesPerGame             float64
	EnemiesFlashedPerGame     float64
	TeammatesFlashedPerGame   float64
	FlashAssistsPerGame       float64
	AvgBlindDuration          float64
	TotalBlindDurationPerGame float64
}

func NewPlayerStats() *PlayerStats {
	return &PlayerStats{
		SurvivalByPhase:    make(map[string]int),
		Velocity:           make(map[string]float64),
		TimeToFirstDamage:  make([]float64, 0),
		CrosshairPlacement: make([]float64, 0),
		WeaponStats:        make(map[string]*WeaponStats),
		MapAreaKills:       make(map[string]int),
		MapAreaDeaths:      make(map[string]int),
		TimeToFirstShot:    make([]float64, 0),
	}
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
