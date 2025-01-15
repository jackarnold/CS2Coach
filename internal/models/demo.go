package models

type Match struct {
	MapName     string
	TickRate    float64
	Events      []Event
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
	}
}

func (m *Match) GetOrCreatePlayerStats(steamID uint64, name string) *PlayerStats {
	stats, exists := m.PlayerStats[steamID]
	if !exists {
		stats = &PlayerStats{
			Name:        name,
			SteamID:     steamID,
			WeaponStats: make(map[string]*WeaponStats),
		}
		m.PlayerStats[steamID] = stats
	}
	return stats
}

func (m *Match) AddEvent(event Event) {
	m.Events = append(m.Events, event)
}
