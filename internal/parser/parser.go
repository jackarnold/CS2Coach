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
	"github.com/markus-wa/demoinfocs-golang/v4/pkg/demoinfocs/msgs2"
	"github.com/richardkiene/CS2Coach/internal/models"
)

type Parser struct {
	debug              bool
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
	currentRoundKills  map[uint64]map[int]int
	lastWeaponFireTime map[uint64]time.Time
}

func NewParser(debug bool) *Parser {
	return &Parser{
		debug:              debug,
		match:              models.NewMatch(),
		alivePlayersByTeam: make(map[int]int),
		currentRoundKills:  make(map[uint64]map[int]int),
		sprayStartTime:     make(map[uint64]time.Time),
		currentSprayShots:  make(map[uint64]int),
		enemySpottedTime:   make(map[uint64]map[uint64]time.Time),
		firstDamageTime:    make(map[uint64]map[uint64]time.Time),
		lastWeaponFireTime: make(map[uint64]time.Time),
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

	p.match = models.NewMatch()
	p.registerEventHandlers(debug)

	fmt.Println("Starting demo parse...")
	err = p.parser.ParseToEnd()
	if err != nil {
		return nil, fmt.Errorf("parse error: %v", err)
	}

	if p.match.MapName == "" {
		p.match.MapName = "Unknown Map"
		if debug {
			fmt.Println("Warning: Could not determine map name")
		}
	}

	fmt.Printf("Finished parsing. Found %d events\n", len(p.match.Events))
	return p.match, nil
}

func magnitude(v r3.Vector) float64 {
	return math.Sqrt(v.X*v.X + v.Y*v.Y + v.Z*v.Z)
}

func (p *Parser) registerEventHandlers(debug bool) {
	// Server info handler
	p.parser.RegisterNetMessageHandler(func(msg *msgs2.CSVCMsg_ServerInfo) {
		p.match.MapName = msg.GetMapName()
		if debug {
			fmt.Printf("Map name from server info: %s\n", *msg.MapName)
		}
	})

	// Match start handler
	p.parser.RegisterEventHandler(func(e events.MatchStart) {
		p.parser.GameState().TotalRoundsPlayed()
		for _, player := range p.parser.GameState().Participants().Playing() {
			if player.SteamID64 == 0 {
				continue
			}
			stats := p.match.GetOrCreatePlayerStats(player.SteamID64, player.Name)
			stats.Team = int(player.Team)
			stats.IsAlive = true
		}
	})

	// Damage tracking
	lastDamageBy := make(map[uint64]map[uint64]int)
	p.parser.RegisterEventHandler(func(e events.PlayerHurt) {
		if e.Attacker == nil || e.Player == nil || e.Attacker.SteamID64 == 0 ||
			e.Player.SteamID64 == 0 || e.Attacker.SteamID64 == e.Player.SteamID64 {
			return
		}

		victimID := e.Player.SteamID64
		attackerID := e.Attacker.SteamID64

		if _, exists := lastDamageBy[victimID]; !exists {
			lastDamageBy[victimID] = make(map[uint64]int)
		}
		lastDamageBy[victimID][attackerID] += e.HealthDamage
	})

	// Kill assist handler
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

	// Spotted enemies handler (abusing for tracking player positions too)
	p.parser.RegisterEventHandler(func(e events.FrameDone) {

		for _, player := range p.parser.GameState().Participants().Playing() {
			for _, enemy := range p.parser.GameState().Participants().Playing() {
				if player.Team == enemy.Team || player.SteamID64 == 0 || enemy.SteamID64 == 0 {
					continue
				}

				stats := p.match.GetOrCreatePlayerStats(player.SteamID64, player.Name)
				stats.Velocity[player.Name] = magnitude(player.Velocity())

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

	// Core event handlers
	p.parser.RegisterEventHandler(p.handleKill)
	p.parser.RegisterEventHandler(p.handleWeaponFire)
	p.parser.RegisterEventHandler(p.handlePlayerHurt)
	p.parser.RegisterEventHandler(p.handleRoundStart)
	p.parser.RegisterEventHandler(p.handleRoundEnd)
	p.parser.RegisterEventHandler(p.handleTrade)
	p.parser.RegisterEventHandler(p.handleUtility)
	p.parser.RegisterEventHandler(p.handleActivity)
	p.parser.RegisterEventHandler(p.handlePeekTracking)
}

func calculateHLTVRating(stats *models.PlayerStats, roundCount int) float64 {
	killRating := float64(stats.Kills) / float64(roundCount) / 0.679
	survivalRating := float64(roundCount-stats.Deaths) / float64(roundCount) / 0.317
	roundsWithMultipleKillsRating := float64(stats.TwoKills+stats.ThreeKills+stats.FourKills+stats.FiveKills) / float64(roundCount) / 1.277

	return (killRating + 0.7*survivalRating + roundsWithMultipleKillsRating) / 2.7
}

func getUtilityValue(grenadeType common.EquipmentType) int {
	switch grenadeType {
	case common.EqHE:
		return 300
	case common.EqFlash:
		return 200
	case common.EqSmoke:
		return 300
	case common.EqMolotov:
		return 400
	case common.EqIncendiary:
		return 600
	default:
		return 0
	}
}

func (p *Parser) handleUtility(e events.GrenadeEvent) {
	if (!p.isLiveGameRound()) || e.Thrower == nil {
		return
	}

	stats := p.match.GetOrCreatePlayerStats(e.Thrower.SteamID64, e.Thrower.Name)

	switch e.Grenade.Type {
	case common.EqFlash:
		stats.UtilityStats.FlashesThrown++
		for _, player := range p.parser.GameState().Participants().Playing() {
			if player.FlashDurationTime() > 0 {
				if player.Team == e.Thrower.Team {
					stats.UtilityStats.TeammatesFlashed++
				} else {
					stats.UtilityStats.EnemiesFlashed++
					stats.UtilityStats.TotalBlindDuration += player.FlashDurationTime().Seconds()
				}
			}
		}
	case common.EqHE:
		stats.UtilityStats.HEGrenadesThrown++
	case common.EqSmoke:
		stats.UtilityStats.SmokesThrown++
	case common.EqMolotov, common.EqIncendiary:
		stats.UtilityStats.MolotovsThrown++
	}

	stats.UnusedUtilityValue += getUtilityValue(e.Grenade.Type)
}

func (p *Parser) handleTrade(e events.Kill) {
	if (!p.isLiveGameRound()) || e.Killer == nil || e.Victim == nil {
		return
	}

	// Check for trade opportunities
	for _, player := range p.parser.GameState().Participants().Playing() {
		if player.Team == e.Victim.Team && player.IsAlive() {
			stats := p.match.GetOrCreatePlayerStats(player.SteamID64, player.Name)
			stats.TradeKillOpportunities++

			// Check if attempt was made (player fired at killer within 3 seconds)
			if time.Since(p.lastWeaponFireTime[player.SteamID64]) <= 3*time.Second {
				stats.TradeKillAttempts++
			}
		}
	}

	// Check if this kill was a trade (victim killed someone in last 3 seconds)
	if p.lastKillTime != nil && time.Since(*p.lastKillTime) <= 3*time.Second {
		if p.lastKillVictim == e.Killer.SteamID64 {
			killerStats := p.match.GetOrCreatePlayerStats(e.Killer.SteamID64, e.Killer.Name)
			killerStats.TradeKills++
		}
	}
}

func (p *Parser) handleActivity(e events.PlayerHurt) {
	if (!p.isLiveGameRound()) || e.Attacker == nil || e.Player == nil {
		return
	}

	stats := p.match.GetOrCreatePlayerStats(e.Attacker.SteamID64, e.Attacker.Name)

	switch e.Weapon.Type {
	case common.EqHE:
		stats.UtilityStats.HEDamage += e.HealthDamage
	case common.EqMolotov, common.EqIncendiary:
		// Track molotov damage
		if e.Attacker.Team == e.Player.Team {
			stats.TeamUtilityDamage += e.HealthDamage
		} else {
			stats.UtilityStats.MolotovsThrown++
		}
	}

	// Track overall damage
	stats.TotalDamage += e.HealthDamage
}

func (p *Parser) handleKill(e events.Kill) {
	if (!p.isLiveGameRound()) || e.Killer == nil || e.Victim == nil || e.Killer.SteamID64 == 0 || e.Victim.SteamID64 == 0 {
		return
	}

	killerStats := p.match.GetOrCreatePlayerStats(e.Killer.SteamID64, e.Killer.Name)
	victimStats := p.match.GetOrCreatePlayerStats(e.Victim.SteamID64, e.Victim.Name)

	killerStats.Kills++
	victimStats.Deaths++
	victimStats.IsAlive = false

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

	if e.IsHeadshot {
		killerStats.Headshots++
	}

	// Multi-kill tracking
	currentRound := p.parser.GameState().TotalRoundsPlayed()
	if _, exists := p.currentRoundKills[e.Killer.SteamID64]; !exists {
		p.currentRoundKills[e.Killer.SteamID64] = make(map[int]int)
	}
	p.currentRoundKills[e.Killer.SteamID64][currentRound]++

	killCount := p.currentRoundKills[e.Killer.SteamID64][currentRound]
	switch killCount {
	case 2:
		killerStats.TwoKills++
	case 3:
		killerStats.ThreeKills++
	case 4:
		killerStats.FourKills++
	case 5:
		killerStats.FiveKills++
	}

	// Trade kill tracking
	now := time.Now()
	if p.lastKillTime != nil && now.Sub(*p.lastKillTime) <= 3*time.Second {
		if p.lastKillKiller != nil && e.Victim.Team != e.Killer.Team {
			if e.Victim.SteamID64 == p.lastKillKiller.SteamID {
				killerStats.TradeKills++
				killerStats.TradeKillAttempts++
				killerStats.TradeKillOpportunities++
			}
		}
	}

	// Update trade opportunities for other team members
	for _, player := range p.parser.GameState().Participants().Playing() {
		if player.Team == e.Victim.Team && player.IsAlive() && player.SteamID64 != e.Victim.SteamID64 {
			stats := p.match.GetOrCreatePlayerStats(player.SteamID64, player.Name)
			stats.TradeKillOpportunities++
		}
	}

	p.lastKillTime = &now
	p.lastKillVictim = e.Victim.SteamID64
	p.lastKillKiller = killerStats
}

func (p *Parser) handleWeaponFire(e events.WeaponFire) {
	if (!p.isLiveGameRound()) || e.Shooter == nil || e.Shooter.SteamID64 == 0 {
		return
	}

	stats := p.match.GetOrCreatePlayerStats(e.Shooter.SteamID64, e.Shooter.Name)
	stats.ShotsTotal++

	// Track spotted shots
	for _, enemy := range p.parser.GameState().Participants().Playing() {
		if enemy.Team != e.Shooter.Team && enemy.IsAlive() && isVisible(e.Shooter, enemy) {
			stats.EnemySpottedShots++
			break
		}
	}

	// Track counter-strafing
	shooter := e.Shooter
	currentVel := magnitude(shooter.Velocity())
	prevVel := stats.Velocity[shooter.Name]

	// Check if we basically dropped from a higher velocity to near zero in a short timespan
	// This is a naive approach:
	if prevVel > 100 && currentVel < 30 {
		stats.CounterStrafedShots++
	}

	p.lastWeaponFireTime[e.Shooter.SteamID64] = time.Now()
}

func (p *Parser) isLiveGameRound() bool {
	return !(p.parser.GameState().IsWarmupPeriod() || p.parser.GameState().IsFreezetimePeriod())
}

func (p *Parser) handlePlayerHurt(e events.PlayerHurt) {
	if (!p.isLiveGameRound()) || e.Attacker == nil || e.Player == nil || e.Attacker.SteamID64 == 0 || e.Player.SteamID64 == 0 {
		return
	}

	stats := p.match.GetOrCreatePlayerStats(e.Attacker.SteamID64, e.Attacker.Name)
	stats.HitsTotal++
	stats.TotalDamage += e.HealthDamage

	// Track first damage time
	if _, exists := p.firstDamageTime[e.Attacker.SteamID64]; !exists {
		p.firstDamageTime[e.Attacker.SteamID64] = make(map[uint64]time.Time)
	}
	if _, recorded := p.firstDamageTime[e.Attacker.SteamID64][e.Player.SteamID64]; !recorded {
		if spottedTime, wasSpotted := p.enemySpottedTime[e.Attacker.SteamID64][e.Player.SteamID64]; wasSpotted {
			timeToHit := time.Since(spottedTime).Seconds()
			stats.TimeToFirstDamage = append(stats.TimeToFirstDamage, timeToHit)
		}
		p.firstDamageTime[e.Attacker.SteamID64][e.Player.SteamID64] = time.Now()
	}

	// Utility damage tracking
	switch e.Weapon.Type {
	case common.EqHE:
		stats.UtilityStats.HEDamage += e.HealthDamage
		if e.Attacker.Team == e.Player.Team {
			stats.TeamUtilityDamage += e.HealthDamage
		}
	case common.EqMolotov, common.EqIncendiary:
		if e.Attacker.Team == e.Player.Team {
			stats.TeamUtilityDamage += e.HealthDamage
		}
	}
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

func (p *Parser) handlePeekTracking(e events.Kill) {
	if (!p.isLiveGameRound()) || e.Killer == nil || e.Victim == nil {
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
	if !p.isLiveGameRound() {
		return
	}

	for _, player := range p.parser.GameState().Participants().Playing() {
		if player.SteamID64 == 0 {
			continue
		}

		stats := p.match.GetOrCreatePlayerStats(player.SteamID64, player.Name)
		stats.EquipmentValue = int(player.EquipmentValueCurrent())
		stats.MoneySpent += stats.EquipmentValue
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
