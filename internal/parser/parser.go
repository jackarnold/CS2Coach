package parser

import (
	"fmt"
	"os"
	"time"

	dem "github.com/markus-wa/demoinfocs-golang/v4/pkg/demoinfocs"
	events "github.com/markus-wa/demoinfocs-golang/v4/pkg/demoinfocs/events"
	"github.com/richardkiene/CS2Coach/internal/models"
)

type Parser struct {
	match              *models.Match
	parser             dem.Parser
	lastKillTime       *time.Time
	lastKillVictim     uint64
	lastKillKiller     *models.PlayerStats
	roundStartTime     time.Time
	alivePlayersByTeam map[int]int
}

func NewParser() *Parser {
	return &Parser{
		match:              models.NewMatch(),
		alivePlayersByTeam: make(map[int]int),
	}
}

func (p *Parser) ParseDemo(path string, debug bool) (*models.Match, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	p.parser = dem.NewParser(f)
	defer p.parser.Close()

	// Register core event handlers first
	p.registerEventHandlers(debug)

	header, err := p.parser.ParseHeader()
	if err != nil {
		return nil, err
	}

	// Initialize match data
	p.match.MapName = header.MapName
	p.match.TickRate = p.parser.TickRate()

	// Track players and their state
	p.parser.RegisterEventHandler(func(e events.MatchStart) {
		for _, player := range p.parser.GameState().Participants().Playing() {
			if player.SteamID64 == 0 {
				continue
			}
			stats := p.match.GetOrCreatePlayerStats(player.SteamID64, player.Name)
			stats.Team = int(player.Team)
			stats.IsAlive = true
		}
	})

	// Track damage for assists
	lastDamageBy := make(map[uint64]map[uint64]int) // victim -> attacker -> damage

	p.parser.RegisterEventHandler(func(e events.PlayerHurt) {
		if e.Attacker == nil || e.Player == nil || e.Attacker.SteamID64 == 0 || e.Player.SteamID64 == 0 || e.Attacker.SteamID64 == e.Player.SteamID64 {
			return
		}

		victimID := e.Player.SteamID64
		attackerID := e.Attacker.SteamID64

		if _, exists := lastDamageBy[victimID]; !exists {
			lastDamageBy[victimID] = make(map[uint64]int)
		}
		lastDamageBy[victimID][attackerID] += e.HealthDamage
	})

	p.parser.RegisterEventHandler(func(e events.Kill) {
		if e.Killer == nil || e.Victim == nil || e.Killer.SteamID64 == 0 || e.Victim.SteamID64 == 0 {
			return
		}

		// Process assists
		victimID := e.Victim.SteamID64
		killerID := e.Killer.SteamID64

		if damages, exists := lastDamageBy[victimID]; exists {
			for attackerID, damage := range damages {
				if attackerID != killerID && damage >= 41 { // Assist threshold: 41+ damage
					for _, player := range p.parser.GameState().Participants().All() {
						if player.SteamID64 == attackerID {
							attackerStats := p.match.GetOrCreatePlayerStats(attackerID, player.Name)
							attackerStats.Assists++
							break
						}
					}
				}
			}
			delete(lastDamageBy, victimID)
		}

		// Track flash assists
		if e.AssistedFlash {
			for _, player := range p.parser.GameState().Participants().Playing() {
				if player.Team != e.Victim.Team && player.FlashDurationTime() > 0 {
					stats := p.match.GetOrCreatePlayerStats(player.SteamID64, player.Name)
					stats.FlashAssists++
					break
				}
			}
		}
	})

	fmt.Println("Starting demo parse...")
	err = p.parser.ParseToEnd()
	if err != nil {
		return nil, fmt.Errorf("parse error: %v", err)
	}
	fmt.Printf("Finished parsing. Found %d events\n", len(p.match.Events))

	return p.match, nil
}

func (p *Parser) registerEventHandlers(debug bool) {
	p.parser.RegisterEventHandler(func(e events.Kill) { p.handleKill(e, debug) })
	p.parser.RegisterEventHandler(p.handleWeaponFire)
	p.parser.RegisterEventHandler(p.handlePlayerHurt)
	p.parser.RegisterEventHandler(p.handleRoundStart)
	p.parser.RegisterEventHandler(p.handleRoundEnd)
}

func (p *Parser) handleKill(e events.Kill, debug bool) {
	if e.Killer == nil || e.Victim == nil || e.Killer.SteamID64 == 0 || e.Victim.SteamID64 == 0 {
		if debug {
			fmt.Printf("Skipping kill event due to invalid data\n")
		}
		return
	}

	if debug {
		fmt.Printf("Processing kill: %s killed %s\n", e.Killer.Name, e.Victim.Name)
	}

	killerStats := p.match.GetOrCreatePlayerStats(e.Killer.SteamID64, e.Killer.Name)
	victimStats := p.match.GetOrCreatePlayerStats(e.Victim.SteamID64, e.Victim.Name)

	killerStats.Kills++
	victimStats.Deaths++
	victimStats.IsAlive = false

	if e.IsHeadshot {
		killerStats.Headshots++
	}

	// Update weapon stats
	if weaponStats := killerStats.GetOrCreateWeaponStats(e.Weapon.String()); weaponStats != nil {
		weaponStats.Kills++
	}

	// Handle trade kills
	now := time.Now()
	if p.lastKillTime != nil && now.Sub(*p.lastKillTime).Seconds() <= 3.0 {
		killerStats.TradeKills++
		if p.lastKillKiller != nil {
			p.lastKillKiller.TimesTraded++
		}
	}
	p.lastKillTime = &now
	p.lastKillVictim = victimStats.SteamID
	p.lastKillKiller = killerStats

	// Update map area stats
	if area := getMapArea(Point{
		X: float32(e.Killer.Position().X),
		Y: float32(e.Killer.Position().Y),
		Z: float32(e.Killer.Position().Z),
	}); area != "" {
		killerStats.MapAreaKills[area]++
	}
	if area := getMapArea(Point{
		X: float32(e.Victim.Position().X),
		Y: float32(e.Victim.Position().Y),
		Z: float32(e.Victim.Position().Z),
	}); area != "" {
		victimStats.MapAreaDeaths[area]++
	}

	p.match.AddEvent(models.Event{
		Type: "kill",
		Data: map[string]interface{}{
			"killer":     e.Killer.Name,
			"victim":     e.Victim.Name,
			"weapon":     e.Weapon.String(),
			"headshot":   e.IsHeadshot,
			"killer_pos": e.Killer.Position(),
			"victim_pos": e.Victim.Position(),
			"timestamp":  time.Now().Unix(),
		},
	})
}

func (p *Parser) handleWeaponFire(e events.WeaponFire) {
	if e.Shooter == nil || e.Shooter.SteamID64 == 0 {
		return
	}

	stats := p.match.GetOrCreatePlayerStats(e.Shooter.SteamID64, e.Shooter.Name)
	stats.ShotsTotal++

	weaponStats := stats.GetOrCreateWeaponStats(e.Weapon.String())
	weaponStats.Shots++

	p.match.AddEvent(models.Event{
		Type: "weapon_fire",
		Data: map[string]interface{}{
			"player":   e.Shooter.Name,
			"weapon":   e.Weapon.String(),
			"position": e.Shooter.Position(),
		},
	})
}

func (p *Parser) handlePlayerHurt(e events.PlayerHurt) {
	if e.Attacker == nil || e.Player == nil || e.Attacker.SteamID64 == 0 || e.Player.SteamID64 == 0 {
		return
	}

	stats := p.match.GetOrCreatePlayerStats(e.Attacker.SteamID64, e.Attacker.Name)
	stats.HitsTotal++

	weaponStats := stats.GetOrCreateWeaponStats(e.Weapon.String())
	weaponStats.Hits++
	weaponStats.Damage += e.HealthDamage

	if isUtilityWeapon(e.Weapon.String()) {
		stats.UtilityDamage += e.HealthDamage
	}

	p.match.AddEvent(models.Event{
		Type: "player_hurt",
		Data: map[string]interface{}{
			"attacker":     e.Attacker.Name,
			"victim":       e.Player.Name,
			"weapon":       e.Weapon.String(),
			"damage":       e.HealthDamage,
			"armor":        e.ArmorDamage,
			"attacker_pos": e.Attacker.Position(),
			"victim_pos":   e.Player.Position(),
		},
	})
}

func (p *Parser) handleRoundStart(e events.RoundStart) {
	p.roundStartTime = time.Now()
	p.alivePlayersByTeam = make(map[int]int)

	// Reset player states and update alive counts
	for _, player := range p.parser.GameState().Participants().Playing() {
		if player.SteamID64 == 0 {
			continue
		}
		stats := p.match.GetOrCreatePlayerStats(player.SteamID64, player.Name)
		stats.IsAlive = true
		p.alivePlayersByTeam[int(player.Team)]++

		if stats.SurvivalByPhase == nil {
			stats.SurvivalByPhase = make(map[string]int)
		}
	}

	p.match.AddEvent(models.Event{
		Type: "round_start",
		Data: map[string]interface{}{
			"timestamp": p.roundStartTime.Unix(),
		},
	})
}

func (p *Parser) handleRoundEnd(e events.RoundEnd) {
	// Update survival stats
	for _, player := range p.parser.GameState().Participants().Playing() {
		if player.SteamID64 == 0 {
			continue
		}
		stats := p.match.GetOrCreatePlayerStats(player.SteamID64, player.Name)
		if stats.IsAlive {
			stats.RoundsSurvived++

			timeInRound := time.Since(p.roundStartTime).Seconds()
			switch {
			case timeInRound < 30:
				stats.SurvivalByPhase["early"]++
			case timeInRound < 60:
				stats.SurvivalByPhase["mid"]++
			default:
				stats.SurvivalByPhase["late"]++
			}
		}
	}

	p.match.AddEvent(models.Event{
		Type: "round_end",
		Data: map[string]interface{}{
			"winner":    e.Winner,
			"reason":    e.Reason,
			"timestamp": time.Now().Unix(),
		},
	})
}

type Point struct {
	X, Y, Z float32
}

func getMapArea(pos Point) string {
	if pos.Z > 200 {
		return "upper"
	} else if pos.Z < -200 {
		return "lower"
	}
	return "mid"
}

func isUtilityWeapon(weapon string) bool {
	utilities := map[string]bool{
		"HE Grenade":    true,
		"Flashbang":     true,
		"Smoke Grenade": true,
		"Molotov":       true,
		"Incendiary":    true,
	}
	return utilities[weapon]
}
