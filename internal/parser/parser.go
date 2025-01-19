package parser

import (
	"fmt"
	"math"
	"os"
	"time"

	"github.com/golang/geo/r3"
	dem "github.com/markus-wa/demoinfocs-golang/v4/pkg/demoinfocs"
	common "github.com/markus-wa/demoinfocs-golang/v4/pkg/demoinfocs/common"
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
	sprayStartTime     map[uint64]time.Time
	currentSprayShots  map[uint64]int
	enemySpottedTime   map[uint64]map[uint64]time.Time
	firstDamageTime    map[uint64]map[uint64]time.Time
	alivePlayersByTeam map[int]int
}

func NewParser() *Parser {
	return &Parser{
		match:              models.NewMatch(),
		alivePlayersByTeam: make(map[int]int),
		sprayStartTime:     make(map[uint64]time.Time),
		currentSprayShots:  make(map[uint64]int),
		enemySpottedTime:   make(map[uint64]map[uint64]time.Time),
		firstDamageTime:    make(map[uint64]map[uint64]time.Time),
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

	p.registerEventHandlers(debug)

	header, err := p.parser.ParseHeader()
	if err != nil {
		return nil, err
	}

	p.match.MapName = header.MapName
	p.match.TickRate = p.parser.TickRate()

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

	lastDamageBy := make(map[uint64]map[uint64]int)

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

		victimID := e.Victim.SteamID64
		killerID := e.Killer.SteamID64

		if damages, exists := lastDamageBy[victimID]; exists {
			for attackerID, damage := range damages {
				if attackerID != killerID && damage >= 41 {
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
	// Handle spotted enemies
	p.parser.RegisterEventHandler(func(e events.FrameDone) {
		for _, player := range p.parser.GameState().Participants().Playing() {
			for _, enemy := range p.parser.GameState().Participants().Playing() {
				if player.Team == enemy.Team || player.SteamID64 == 0 || enemy.SteamID64 == 0 {
					continue
				}

				if player.IsAlive() && enemy.IsAlive() && isVisible(player, enemy) {
					spotterID := player.SteamID64
					spottedID := enemy.SteamID64

					if _, exists := p.enemySpottedTime[spotterID]; !exists {
						p.enemySpottedTime[spotterID] = make(map[uint64]time.Time)
					}
					if _, exists := p.enemySpottedTime[spotterID][spottedID]; !exists {
						p.enemySpottedTime[spotterID][spottedID] = time.Now()
					}
				}
			}
		}
	})

	// Handle weapon fires
	p.parser.RegisterEventHandler(func(e events.WeaponFire) {
		if e.Shooter == nil || e.Shooter.SteamID64 == 0 {
			return
		}

		shooterID := e.Shooter.SteamID64
		stats := p.match.GetOrCreatePlayerStats(shooterID, e.Shooter.Name)

		// Check spotted enemies
		for _, player := range p.parser.GameState().Participants().Playing() {
			if player.Team == e.Shooter.Team {
				continue
			}
			if spottedTime, exists := p.enemySpottedTime[shooterID][player.SteamID64]; exists {
				if time.Since(spottedTime) < 2*time.Second {
					stats.EnemySpottedShots++
					break
				}
			}
		}

		// Track spray patterns
		if _, exists := p.sprayStartTime[shooterID]; !exists {
			p.sprayStartTime[shooterID] = time.Now()
			p.currentSprayShots[shooterID] = 1
		} else if time.Since(p.sprayStartTime[shooterID]) < 500*time.Millisecond {
			p.currentSprayShots[shooterID]++
			if p.currentSprayShots[shooterID] >= 3 {
				stats.SprayShots++
			}
		} else {
			p.sprayStartTime[shooterID] = time.Now()
			p.currentSprayShots[shooterID] = 1
		}

		// Counter-strafing check
		velocity := e.Shooter.Velocity()
		speed := math.Sqrt(velocity.X*velocity.X + velocity.Y*velocity.Y + velocity.Z*velocity.Z)
		if speed < 30 {
			stats.CounterStrafedShots++
		}
	})

	// Handle hits and damage
	p.parser.RegisterEventHandler(func(e events.PlayerHurt) {
		if e.Attacker == nil || e.Player == nil || e.Attacker.SteamID64 == 0 || e.Player.SteamID64 == 0 {
			return
		}

		attackerID := e.Attacker.SteamID64
		victimID := e.Player.SteamID64
		stats := p.match.GetOrCreatePlayerStats(attackerID, e.Attacker.Name)

		// Check spotted enemy hits
		if spottedTime, exists := p.enemySpottedTime[attackerID][victimID]; exists {
			if time.Since(spottedTime) < 2*time.Second {
				stats.EnemySpottedHits++
			}
		}

		// Track spray hits
		if p.currentSprayShots[attackerID] >= 3 {
			stats.SprayHits++
		}

		// First damage timing
		if _, exists := p.firstDamageTime[attackerID]; !exists {
			p.firstDamageTime[attackerID] = make(map[uint64]time.Time)
		}
		if _, exists := p.firstDamageTime[attackerID][victimID]; !exists {
			if spottedTime, exists := p.enemySpottedTime[attackerID][victimID]; exists {
				timeToDamage := time.Since(spottedTime).Seconds()
				stats.TimeToFirstDamage = append(stats.TimeToFirstDamage, timeToDamage)
				p.firstDamageTime[attackerID][victimID] = time.Now()
			}
		}

		stats.TotalDamage += e.HealthDamage
	})

	// Handle kills and trades
	p.parser.RegisterEventHandler(func(e events.Kill) {
		if e.Killer == nil || e.Victim == nil || e.Killer.SteamID64 == 0 || e.Victim.SteamID64 == 0 {
			return
		}

		// Check for trades
		if p.lastKillTime != nil && time.Since(*p.lastKillTime) < 3*time.Second {
			if p.lastKillVictim == e.Killer.SteamID64 {
				victimStats := p.match.GetOrCreatePlayerStats(e.Victim.SteamID64, e.Victim.Name)
				victimStats.TradedDeaths++
			}
		}
	})

	// Handle flash events
	p.parser.RegisterEventHandler(func(e events.FlashExplode) {
		if e.Thrower == nil || e.Thrower.SteamID64 == 0 {
			return
		}

		stats := p.match.GetOrCreatePlayerStats(e.Thrower.SteamID64, e.Thrower.Name)

		enemiesFlashed := 0
		totalBlindDuration := 0.0

		for _, player := range p.parser.GameState().Participants().Playing() {
			if player.Team != e.Thrower.Team && player.FlashDurationTime() > 0 {
				enemiesFlashed++
				totalBlindDuration += player.FlashDurationTime().Seconds()
			}
		}

		stats.UtilityStats.EnemiesFlashed += enemiesFlashed
		stats.UtilityStats.TotalBlindDuration += totalBlindDuration
	})

	// Register existing handlers
	p.parser.RegisterEventHandler(func(e events.Kill) {
		p.handleKill(e, debug)
		p.handleSprayControl(e)
		p.handlePeekTracking(e)
	})

	p.parser.RegisterEventHandler(func(e events.WeaponFire) {
		p.handleWeaponFire(e)
		p.handleFirstBulletAccuracy(e)
	})

	p.parser.RegisterEventHandler(p.handlePlayerHurt)

	p.parser.RegisterEventHandler(func(e events.RoundStart) {
		p.handleRoundStart(e)
		p.handleEquipmentTracking(e)
	})

	p.parser.RegisterEventHandler(p.handleRoundEnd)

	p.parser.RegisterEventHandler(func(e events.GrenadeEvent) {
		if e.Thrower != nil {
			stats := p.match.GetOrCreatePlayerStats(e.Thrower.SteamID64, e.Thrower.Name)
			if e.Grenade.Type == common.EqFlash {
				stats.UtilityStats.FlashesThrown++
			} else if e.Grenade.Type == common.EqSmoke {
				stats.UtilityStats.SmokesThrown++
			}
		}
	})
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

	if weaponStats := killerStats.GetOrCreateWeaponStats(e.Weapon.String()); weaponStats != nil {
		weaponStats.Kills++
	}

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

func (p *Parser) handleFirstBulletAccuracy(e events.WeaponFire) {
	if e.Shooter == nil {
		return
	}

	stats := p.match.GetOrCreatePlayerStats(e.Shooter.SteamID64, e.Shooter.Name)
	stats.FirstBulletShots++

	go func() {
		time.Sleep(100 * time.Millisecond)
		if stats.HitsTotal > 0 {
			stats.FirstBulletHits++
		}
	}()
}

func (p *Parser) handleSprayControl(e events.Kill) {
	if e.Killer == nil || p.lastKillKiller == nil {
		return
	}

	if e.Killer.SteamID64 == p.lastKillKiller.SteamID &&
		time.Since(*p.lastKillTime) < time.Second {
		p.lastKillKiller.SprayTransfers++
	}
}

func (p *Parser) handlePeekTracking(e events.Kill) {
	if e.Killer == nil || e.Victim == nil {
		return
	}

	killerStats := p.match.GetOrCreatePlayerStats(e.Killer.SteamID64, e.Killer.Name)

	killerPos := e.Killer.Position()
	victimPos := e.Victim.Position()
	killerAngle := e.Killer.ViewDirectionX()

	if isQuickPeek(killerPos, victimPos, killerAngle) {
		killerStats.PeekKills++
	}
}

func isQuickPeek(killerPos, victimPos r3.Vector, killerAngle float32) bool {
	deltaX := victimPos.X - killerPos.X
	deltaY := victimPos.Y - killerPos.Y

	angle := float32(math.Atan2(float64(deltaY), float64(deltaX))) * 180 / math.Pi
	angleDiff := math.Abs(float64(angle - killerAngle))

	return angleDiff <= 45
}

func (p *Parser) handleEquipmentTracking(e events.RoundStart) {
	for _, player := range p.parser.GameState().Participants().Playing() {
		if player.SteamID64 == 0 {
			continue
		}

		stats := p.match.GetOrCreatePlayerStats(player.SteamID64, player.Name)
		stats.EquipmentValue = int(player.EquipmentValueCurrent())

		if stats.EquipmentValue < 4000 && stats.EquipmentValue > 2000 {
			stats.MoneySpent += stats.EquipmentValue
		}
	}
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

func isVisible(observer, target *common.Player) bool {
	observerPos := observer.Position()
	targetPos := target.Position()

	// Create a ray from observer to target
	ray := r3.Vector{
		X: targetPos.X - observerPos.X,
		Y: targetPos.Y - observerPos.Y,
		Z: targetPos.Z - observerPos.Z,
	}

	// Simple line of sight check
	maxDistance := math.Sqrt(ray.X*ray.X + ray.Y*ray.Y + ray.Z*ray.Z)
	return maxDistance < 1000 // Arbitrary visibility range
}
