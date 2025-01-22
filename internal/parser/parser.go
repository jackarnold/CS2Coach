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
	angleHistory       map[uint64][]float32
	lastKnownHP        map[uint64]int // steamID -> current HP
	lastDamageBy       map[uint64]map[uint64]int
}

func NewParser(debug bool) *Parser {
	return &Parser{
		debug:              debug,
		match:              models.NewMatch(),
		lastDamageBy:       make(map[uint64]map[uint64]int),
		alivePlayersByTeam: make(map[int]int),
		currentRoundKills:  make(map[uint64]map[int]int),
		sprayStartTime:     make(map[uint64]time.Time),
		currentSprayShots:  make(map[uint64]int),
		enemySpottedTime:   make(map[uint64]map[uint64]time.Time),
		firstDamageTime:    make(map[uint64]map[uint64]time.Time),
		lastWeaponFireTime: make(map[uint64]time.Time),
		angleHistory:       make(map[uint64][]float32),
		lastKnownHP:        make(map[uint64]int),
	}
}

func (p *Parser) GetOrCreatePlayerStats(steamID uint64, name string) *models.PlayerStats {
	stats := p.match.GetOrCreatePlayerStats(steamID, name)
	if stats.TimeToFirstDamage == nil {
		stats.TimeToFirstDamage = make([]float64, 0)
	}
	if stats.CrosshairPlacement == nil {
		stats.CrosshairPlacement = make([]float64, 0)
	}
	if stats.Velocity == nil {
		stats.Velocity = make(map[string]float64)
	}
	return stats
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
	p.parser.RegisterNetMessageHandler(p.handleServerInfo)
	p.parser.RegisterEventHandler(p.handleMatchStart)
	p.parser.RegisterEventHandler(p.handleKill)
	p.parser.RegisterEventHandler(p.handleWeaponFire)
	p.parser.RegisterEventHandler(p.handlePlayerHurt)
	p.parser.RegisterEventHandler(p.handleRoundStart)
	p.parser.RegisterEventHandler(p.handleRoundEnd)
	p.parser.RegisterEventHandler(p.handleTrade)
	p.parser.RegisterEventHandler(p.handleUtility)
	p.parser.RegisterEventHandler(p.handleActivity)
	p.parser.RegisterEventHandler(p.handlePeekTracking)
	p.parser.RegisterEventHandler(p.handleFrameDone)
}

func (p *Parser) handleServerInfo(msg *msgs2.CSVCMsg_ServerInfo) {
	p.match.MapName = msg.GetMapName()
}

func (p *Parser) handleMatchStart(e events.MatchStart) {
	p.parser.GameState().TotalRoundsPlayed()
	for _, player := range p.parser.GameState().Participants().Playing() {
		if player.SteamID64 == 0 {
			continue
		}
		stats := p.match.GetOrCreatePlayerStats(player.SteamID64, player.Name)
		stats.Team = int(player.Team)
		stats.IsAlive = true
	}
}

func (p *Parser) handleKill(e events.Kill) {
	if (!p.isLiveGameRound()) || e.Killer == nil || e.Victim == nil ||
		e.Killer.SteamID64 == 0 || e.Victim.SteamID64 == 0 {
		return
	}

	if p.debug {
		fmt.Printf("Kill Event: %s killed %s (Round: %d, IsLive: %v)\n",
			e.Killer.Name, e.Victim.Name,
			p.parser.GameState().TotalRoundsPlayed(),
			!p.parser.GameState().IsWarmupPeriod() && !p.parser.GameState().IsFreezetimePeriod())
	}

	killerStats := p.match.GetOrCreatePlayerStats(e.Killer.SteamID64, e.Killer.Name)
	victimStats := p.match.GetOrCreatePlayerStats(e.Victim.SteamID64, e.Victim.Name)

	killerStats.Kills++
	victimStats.Deaths++
	victimStats.IsAlive = false

	// Handle kill assists
	victimID := e.Victim.SteamID64
	killerID := e.Killer.SteamID64

	now := time.Now()
	p.lastKillTime = &now
	p.lastKillVictim = victimID
	p.lastKillKiller = killerStats

	if damages, exists := p.lastDamageBy[victimID]; exists {
		assistGiven := make(map[uint64]bool)
		for attackerID, damage := range damages {
			if attackerID != killerID && damage >= 41 && !assistGiven[attackerID] {
				attackerStats := p.match.GetOrCreatePlayerStats(attackerID, "")
				attackerStats.Assists++
				assistGiven[attackerID] = true
			}
		}
		delete(p.lastDamageBy, victimID)
	}

	// Handle flash assists
	if e.AssistedFlash {
		for _, player := range p.parser.GameState().Participants().Playing() {
			if player.Team != e.Victim.Team && player.FlashDurationTime() > 0 &&
				player.SteamID64 != killerID {
				stats := p.match.GetOrCreatePlayerStats(player.SteamID64, player.Name)
				stats.Assists++
				break
			}
		}
	}

	p.updateMultiKills(e)
	p.updateMapAreaStats(e)
}

func (p *Parser) updateMultiKills(e events.Kill) {
	currentRound := p.parser.GameState().TotalRoundsPlayed()
	if _, exists := p.currentRoundKills[e.Killer.SteamID64]; !exists {
		p.currentRoundKills[e.Killer.SteamID64] = make(map[int]int)
	}
	p.currentRoundKills[e.Killer.SteamID64][currentRound]++

	killCount := p.currentRoundKills[e.Killer.SteamID64][currentRound]
	killerStats := p.match.GetOrCreatePlayerStats(e.Killer.SteamID64, e.Killer.Name)

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
}

func (p *Parser) updateMapAreaStats(e events.Kill) {
	killerStats := p.match.GetOrCreatePlayerStats(e.Killer.SteamID64, e.Killer.Name)
	victimStats := p.match.GetOrCreatePlayerStats(e.Victim.SteamID64, e.Victim.Name)

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
}

func (p *Parser) handleFrameDone(e events.FrameDone) {
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

	// Record trade opportunities and attempts
	for _, player := range p.parser.GameState().Participants().Playing() {
		if player.Team == e.Victim.Team && player.IsAlive() {
			stats := p.match.GetOrCreatePlayerStats(player.SteamID64, player.Name)
			stats.TradeKillOpportunities++

			if time.Since(p.lastWeaponFireTime[player.SteamID64]) <= 3*time.Second {
				stats.TradeKillAttempts++
			}
		}
	}

	// Record traded deaths
	victim := p.match.GetOrCreatePlayerStats(e.Victim.SteamID64, e.Victim.Name)
	victim.TradedDeathOpportunities++

	if time.Since(p.lastWeaponFireTime[e.Victim.SteamID64]) <= 3*time.Second {
		victim.TradedDeathAttempts++

		// Check if the death was actually traded
		if p.lastKillTime != nil && time.Since(*p.lastKillTime) <= 3*time.Second {
			if p.lastKillVictim == e.Killer.SteamID64 {
				victim.TradedDeaths++
			}
		}
	}
}

// TODO: This function is being treated as utility stats only but I am fairly sure that's not all we should be tracking.
// -- handlePlayerHurt also tracks this event and really these should be consolidated
func (p *Parser) handleActivity(e events.PlayerHurt) {
	if (!p.isLiveGameRound()) || e.Attacker == nil || e.Player == nil {
		return
	}

	stats := p.match.GetOrCreatePlayerStats(e.Attacker.SteamID64, e.Attacker.Name)

	switch e.Weapon.Type {
	case common.EqHE:
		stats.UtilityStats.HEDamage += e.HealthDamage
	case common.EqMolotov, common.EqIncendiary:
		if e.Attacker.Team == e.Player.Team {
			stats.TeamUtilityDamage += e.HealthDamage
		} else {
			stats.UtilityStats.MolotovsThrown++
			// TODO: track molly damage
		}
	}
}

func (p *Parser) handleWeaponFire(e events.WeaponFire) {
	if (!p.isLiveGameRound()) || e.Shooter == nil || e.Shooter.SteamID64 == 0 {
		return
	}

	stats := p.match.GetOrCreatePlayerStats(e.Shooter.SteamID64, e.Shooter.Name)

	if stats.Velocity == nil {
		stats.Velocity = make(map[string]float64)
	}
	if stats.TimeToFirstDamage == nil {
		stats.TimeToFirstDamage = make([]float64, 0)
	}

	stats.ShotsTotal++

	// Track spotted shots
	enemySpotted := false
	for _, enemy := range p.parser.GameState().Participants().Playing() {
		if enemy.Team != e.Shooter.Team && enemy.IsAlive() && isVisible(e.Shooter, enemy) {
			enemySpotted = true
			break
		}
	}

	if enemySpotted {
		stats.EnemySpottedShots++
	}

	// Track spray control
	if p.currentSprayShots[e.Shooter.SteamID64] > 0 {
		stats.SprayShots++
	}

	// Track counter-strafing
	shooter := e.Shooter
	currentVel := magnitude(shooter.Velocity())
	prevVel := stats.Velocity[shooter.Name]

	if prevVel > 100 && currentVel < 20 {
		stats.CounterStrafedShots++
	}

	p.lastWeaponFireTime[e.Shooter.SteamID64] = time.Now()
}

func (p *Parser) isLiveGameRound() bool {
	gs := p.parser.GameState()
	return !gs.IsWarmupPeriod() && gs.TotalRoundsPlayed() >= 0
}

func (p *Parser) handlePlayerHurt(e events.PlayerHurt) {
	currentRound := p.parser.GameState().TotalRoundsPlayed()
	if p.debug {
		fmt.Printf("Processing damage event in round %d\n", currentRound)
	}

	if (!p.isLiveGameRound()) || e.Attacker == nil || e.Player == nil ||
		e.Attacker.SteamID64 == 0 || e.Player.SteamID64 == 0 {
		fmt.Printf("Skipping damage event, isLiveGameRound=%v, attacker=%v, player=%v\n",
			p.isLiveGameRound(), e.Attacker != nil, e.Player != nil)
		return
	}

	// Debug info
	killerName := "unknown"
	victimName := "unknown"
	if e.Attacker != nil {
		killerName = e.Attacker.Name
	}
	if e.Player != nil {
		victimName = e.Player.Name
	}

	if killerName == "shmeeny" {
		fmt.Printf("Damage Event:\n")
		fmt.Printf("  Attacker: %s (Team: %v)\n", killerName, e.Attacker.Team)
		fmt.Printf("  Victim: %s (Team: %v, Alive: %v)\n", victimName, e.Player.Team, e.Player.IsAlive())
		fmt.Printf("  Damage: %d\n", min(e.HealthDamage, e.Player.Health()))
		fmt.Printf("  Weapon: %v\n", e.Weapon.Type)
		fmt.Printf("  Round State: Warmup=%v, Freeze=%v\n", p.parser.GameState().IsWarmupPeriod(), p.parser.GameState().IsFreezetimePeriod())
		fmt.Printf("  Current Round: %d\n\n", p.parser.GameState().TotalRoundsPlayed())
	}

	stats := p.match.GetOrCreatePlayerStats(e.Attacker.SteamID64, e.Attacker.Name)

	// exclude bomb damage
	if e.Weapon.Type != common.EqBomb {
		victimID := e.Player.SteamID64

		// Track damage for assists
		if e.Attacker.Team != e.Player.Team && e.Attacker.SteamID64 != e.Player.SteamID64 {
			if _, exists := p.lastDamageBy[e.Player.SteamID64]; !exists {
				p.lastDamageBy[e.Player.SteamID64] = make(map[uint64]int)
			}
			p.lastDamageBy[e.Player.SteamID64][e.Attacker.SteamID64] += e.HealthDamage
		}

		// "Old HP" we have stored
		oldHP := p.lastKnownHP[victimID]

		// Potential bullet damage from the event
		rawDamage := e.HealthDamage

		// Net actual HP lost is at most what the victim had left
		actualDamage := min(rawDamage, oldHP)

		// Don't count friendly fire or self damage toward the players total damage done
		if e.Attacker.Team != e.Player.Team && e.Attacker.SteamID64 != e.Player.SteamID64 {
			stats.TotalDamage += actualDamage
		}

		// Deduct from victim's stored HP
		newHP := max(oldHP-actualDamage, 0)
		p.lastKnownHP[victimID] = newHP
	}

	stats.HitsTotal++

	if e.HitGroup == events.HitGroupHead {
		stats.Headshots++
	}

	// Track spotted hits
	if _, exists := p.enemySpottedTime[e.Attacker.SteamID64]; exists {
		if _, spotted := p.enemySpottedTime[e.Attacker.SteamID64][e.Player.SteamID64]; spotted {
			stats.EnemySpottedHits++
		}
	}

	// Track spray hits
	if p.currentSprayShots[e.Attacker.SteamID64] > 0 {
		stats.SprayHits++
	}

	// Track time to damage
	if spottedTime, wasSpotted := p.enemySpottedTime[e.Attacker.SteamID64][e.Player.SteamID64]; wasSpotted {
		if _, alreadyDamaged := p.firstDamageTime[e.Attacker.SteamID64][e.Player.SteamID64]; !alreadyDamaged {
			timeToHit := time.Since(spottedTime).Seconds()
			stats.TimeToFirstDamage = append(stats.TimeToFirstDamage, timeToHit)

			if _, exists := p.firstDamageTime[e.Attacker.SteamID64]; !exists {
				p.firstDamageTime[e.Attacker.SteamID64] = make(map[uint64]time.Time)
			}
			p.firstDamageTime[e.Attacker.SteamID64][e.Player.SteamID64] = time.Now()
		}
	}
}

func (p *Parser) handleRoundStart(e events.RoundStart) {
	p.roundStartTime = time.Now()
	p.alivePlayersByTeam = make(map[int]int)
	p.currentRoundKills = make(map[uint64]map[int]int)         // Reset multi-kill tracking
	p.sprayStartTime = make(map[uint64]time.Time)              // Reset spray tracking
	p.currentSprayShots = make(map[uint64]int)                 // Reset spray shots
	p.enemySpottedTime = make(map[uint64]map[uint64]time.Time) // Reset spotted time
	p.firstDamageTime = make(map[uint64]map[uint64]time.Time)  // Reset damage time
	p.lastWeaponFireTime = make(map[uint64]time.Time)          // Reset weapon fire time
	p.lastDamageBy = make(map[uint64]map[uint64]int)           // Reset damage tracking

	// Reset all player damage counts
	for _, player := range p.parser.GameState().Participants().Playing() {
		if player.SteamID64 == 0 {
			continue
		}
		stats := p.match.GetOrCreatePlayerStats(player.SteamID64, player.Name)
		stats.IsAlive = true
		p.alivePlayersByTeam[int(player.Team)]++

		// Reset player health to 100 on round start
		p.lastKnownHP[player.SteamID64] = 100

		if p.parser.GameState().IsWarmupPeriod() {
			stats.TotalDamage = 0 //Ensure that we don't count warmup damage
			stats.SurvivalByPhase = make(map[string]int)
			stats.RoundsActive = 0
			stats.Kills = 0
			stats.Deaths = 0
			stats.Assists = 0
			continue
		}

		if !p.parser.GameState().IsWarmupPeriod() {
			stats.RoundsActive++
		}
	}

	p.match.AddEvent(models.Event{
		Type: "round_start",
		Data: map[string]interface{}{
			"timestamp":    p.roundStartTime.Unix(),
			"round_number": p.parser.GameState().TotalRoundsPlayed(),
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
