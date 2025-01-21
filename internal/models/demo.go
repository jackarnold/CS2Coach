package models

import "time"

type Match struct {
	MapName     string
	TickRate    float64
	Events      []Event
	Date        string
	PlayerStats map[uint64]*PlayerStats // SteamID64 -> Stats
}

type Event struct {
	Type string
	Data map[string]interface{}
}

func NewMatch() *Match {
	return &Match{
		Events:      make([]Event, 0),
		PlayerStats: make(map[uint64]*PlayerStats),
		Date:        time.Now().Format("2006-01-02"),
	}
}

func (m *Match) GetOrCreatePlayerStats(steamID uint64, name string) *PlayerStats {
	// First try by SteamID if provided
	if steamID != 0 {
		if stats, exists := m.PlayerStats[steamID]; exists {
			return stats
		}
	}

	// If no SteamID or not found, try to find by name
	for _, stats := range m.PlayerStats {
		if stats.Name == name {
			return stats
		}
	}

	// If neither found, create new stats
	stats := &PlayerStats{
		Name:            name,
		SteamID:         steamID,
		WeaponStats:     make(map[string]*WeaponStats),
		SurvivalByPhase: make(map[string]int),
		MapAreaKills:    make(map[string]int),
		MapAreaDeaths:   make(map[string]int),
		Velocity:        make(map[string]float64),
	}

	if steamID != 0 {
		m.PlayerStats[steamID] = stats
	}
	return stats
}

func (m *Match) AddEvent(event Event) {
	m.Events = append(m.Events, event)
}
