package parser

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"time"

	"github.com/golang/geo/r3"
	dem "github.com/markus-wa/demoinfocs-golang/v4/pkg/demoinfocs"
	common "github.com/markus-wa/demoinfocs-golang/v4/pkg/demoinfocs/common"
	events "github.com/markus-wa/demoinfocs-golang/v4/pkg/demoinfocs/events"
	"github.com/markus-wa/demoinfocs-golang/v4/pkg/demoinfocs/msgs2"
	"github.com/richardkiene/CS2Coach/internal/models"
)

type PlayerFrameData struct {
	SteamID    uint64
	Position   r3.Vector
	ViewAngleX float32
	IsAlive    bool
	// TODO: Possibly store bounding box corners for finer geometry checks
}

type FrameData struct {
	Tick          int
	Players       []PlayerFrameData
	VisibilityMap map[uint64]map[uint64]bool // "is this observer -> target visible at this frame"
}

type FrameStorage struct {
	frames []FrameData
}

type SpottedState struct {
	LastSpottedTime time.Time
	LastVisiblePos  r3.Vector
	ViewAngle       float32
}

type Parser struct {
	debug             bool
	match             *models.Match
	parser            dem.Parser
	lastKillTime      *time.Time
	lastKillVictim    uint64
	lastKillKiller    *models.PlayerStats
	roundStartTime    time.Time
	sprayStartTime    map[uint64]time.Time
	currentSprayShots map[uint64]int
	// Map of attacker -> (victim -> time that victim was first spotted this round)
	enemySpottedTime   map[uint64]map[uint64]int
	firstDamageTime    map[uint64]map[uint64]int
	alivePlayersByTeam map[int]int
	currentRoundKills  map[uint64]map[int]int
	lastWeaponFireTime map[uint64]time.Time
	angleHistory       map[uint64][]float32
	lastKnownHP        map[uint64]int // steamID -> current HP
	lastDamageBy       map[uint64]map[uint64]int
	visibilityCache    map[uint64]map[uint64]bool // Short-term cache for expensive visibility checks
	lastUpdateTime     time.Time
	smokePositions     []r3.Vector          // Track active smoke positions
	flashedPlayers     map[uint64]time.Time // Track flashed players
	frameStorage       FrameStorage
	BspChecker         *BSPVisibilityChecker
	warnedBspChecker   bool // Tracks if the warning has been logged
	lastProcessedTick  int
	currentTick        int
	visibilityStats    map[uint64]int
	spottedStats       map[uint64]int
	mapNameFound       bool
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
		enemySpottedTime:   make(map[uint64]map[uint64]int),
		firstDamageTime:    make(map[uint64]map[uint64]int),
		lastWeaponFireTime: make(map[uint64]time.Time),
		angleHistory:       make(map[uint64][]float32),
		lastKnownHP:        make(map[uint64]int),
		visibilityCache:    make(map[uint64]map[uint64]bool),
		flashedPlayers:     make(map[uint64]time.Time),
		lastProcessedTick:  -1,
		frameStorage:       FrameStorage{},
		mapNameFound:       false,
	}
}

func (p *Parser) IsReady() bool {
	return p.BspChecker != nil
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
	// Open the demo file
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	p.parser = dem.NewParser(f)
	defer p.parser.Close()

	p.match = models.NewMatch()
	p.registerEventHandlers(debug)

	if debug {
		fmt.Println("[DEBUG] Starting demo parse...")
	}

	// Step 1: Parse until the map name is found
	for !p.mapNameFound {
		moreFrames, err := p.parser.ParseNextFrame() // Parse the next frame to process events
		if err != nil || !moreFrames {
			if err == dem.ErrUnexpectedEndOfDemo {
				return nil, fmt.Errorf("unable to determine map name from demo file")
			}
			return nil, fmt.Errorf("error during initial parsing: %v", err)
		}
	}

	if debug {
		fmt.Printf("[DEBUG] Map name detected: %s\n", p.match.MapName)
	}

	// Step 2: Load BSP data
	cs2Path := p.GetCS2Path()
	if cs2Path == "" {
		if debug {
			fmt.Println("[DEBUG] Warning: Could not locate CS2 installation")
		}
		return p.match, nil
	}

	loader := NewBSPLoader(cs2Path)
	bspChecker, err := loader.LoadBSPForMap(p.match.MapName)
	if err != nil {
		if debug {
			fmt.Printf("[DEBUG] Warning: Failed to load BSP data for map %s: %v\n", p.match.MapName, err)
		}
		// Continue parsing without BSP data
	} else {
		p.BspChecker = bspChecker
		if debug {
			fmt.Printf("[DEBUG] Successfully loaded BSP data for map %s\n", p.match.MapName)
		}
	}

	// Step 3: Resume parsing events
	if debug {
		fmt.Println("[DEBUG] Resuming full parsing...")
	}
	if err := p.parser.ParseToEnd(); err != nil {
		return nil, fmt.Errorf("error during full parsing: %v", err)
	}

	if debug {
		fmt.Printf("[DEBUG] Finished parsing. Found %d events in total.\n", len(p.match.Events))
	}

	return p.match, nil
}

func (p *Parser) extractMapName(debug bool) (bool, *models.Match, error) {
	header, err := p.parser.ParseHeader()
	if err != nil {
		return true, nil, fmt.Errorf("failed to parse demo header: %v", err)
	}

	// Attempt to extract map name from the header
	mapName := header.MapName
	if mapName != "" {
		p.match.MapName = mapName
		if debug {
			fmt.Printf("[DEBUG] Map detected from demo header: %s\n", mapName)
		}
	} else if debug {
		fmt.Println("[DEBUG] Map name not found in demo header; will attempt to extract from events.")
	}
	return false, nil, nil
}

func (p *Parser) GetCS2Path() string {
	paths := []string{
		`C:\Program Files (x86)\Steam\steamapps\common\Counter-Strike Global Offensive\game\csgo`,
		`C:\Program Files\Steam\steamapps\common\Counter-Strike Global Offensive\game\csgo`,
	}

	for _, path := range paths {
		if fileExists(filepath.Join(path, "maps")) {
			return path
		}
	}

	return "" // Path not found
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
	p.parser.RegisterEventHandler(p.handleFlashEvent)
}

func (p *Parser) handleRoundStart(e events.RoundStart) {
	// Reset round-specific data
	p.lastKnownHP = make(map[uint64]int)
	p.enemySpottedTime = make(map[uint64]map[uint64]int)
	p.firstDamageTime = make(map[uint64]map[uint64]int)
	p.flashedPlayers = make(map[uint64]time.Time)
	p.smokePositions = []r3.Vector{}

	// Update player stats for the new round
	for _, player := range p.parser.GameState().Participants().Playing() {
		if player.SteamID64 == 0 {
			continue
		}

		stats := p.match.GetOrCreatePlayerStats(player.SteamID64, player.Name)
		stats.IsAlive = true
		stats.RoundsActive++
		if p.debug {
			fmt.Printf("[DEBUG] handleRoundStart: Player %s (SteamID: %d), RoundsActive => %d\n",
				player.Name, player.SteamID64, stats.RoundsActive)
		}

		// Initialize HP to 100 for all players
		p.lastKnownHP[player.SteamID64] = 100
	}

	if p.debug {
		fmt.Printf("[DEBUG] Round %d started at tick %d\n",
			p.parser.GameState().TotalRoundsPlayed(),
			p.parser.GameState().IngameTick())
	}

	// Add round_start event to match events
	p.match.AddEvent(models.Event{
		Type: "round_start",
		Data: map[string]interface{}{
			"timestamp":    time.Now().Unix(),
			"round_number": p.parser.GameState().TotalRoundsPlayed(),
		},
	})
}

func (p *Parser) handleFlashEvent(e events.PlayerFlashed) {
	if (!p.isLiveGameRound()) || e.Player == nil {
		return
	}

	oldEnd, had := p.flashedPlayers[e.Player.SteamID64]
	newEnd := time.Now().Add(e.Player.FlashDurationTime())
	if !had || newEnd.After(oldEnd) {
		p.flashedPlayers[e.Player.SteamID64] = newEnd
	}
}

func (p *Parser) handleServerInfo(msg *msgs2.CSVCMsg_ServerInfo) {
	mapName := msg.GetMapName()
	if !p.mapNameFound && mapName != "" {
		p.match.MapName = mapName
		p.mapNameFound = true
		if p.debug {
			fmt.Printf("[DEBUG] Map name detected from server info: %s\n", p.match.MapName)
		}
	}
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
		fmt.Printf("[DEBUG] Kill Event: %s killed %s (Round: %d, IsLive: %v)\n",
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
	p.updateSmokes(p.parser.GameState())
	p.trackPerFramePlayerData(p.parser.GameState())
}

func (p *Parser) trackPerFramePlayerData(gs dem.GameState) {
	p.currentTick = gs.IngameTick()

	// Ensure frame-level processing happens only once per tick
	if p.lastProcessedTick == p.currentTick {
		return
	}
	p.lastProcessedTick = p.currentTick

	frameData := FrameData{
		Tick:          p.currentTick,
		Players:       []PlayerFrameData{},
		VisibilityMap: map[uint64]map[uint64]bool{},
	}

	// Gather data for each player
	for _, player := range gs.Participants().Playing() {
		if player.SteamID64 == 0 {
			continue
		}

		pFrame := PlayerFrameData{
			SteamID:    player.SteamID64,
			Position:   player.Position(),
			ViewAngleX: player.ViewDirectionX(),
			IsAlive:    player.IsAlive(),
		}
		frameData.Players = append(frameData.Players, pFrame)
	}

	// Perform visibility checks only if BSP is loaded
	if p.BspChecker != nil {
		for i := range frameData.Players {
			obs := frameData.Players[i]
			if !obs.IsAlive {
				continue
			}

			if _, ok := frameData.VisibilityMap[obs.SteamID]; !ok {
				frameData.VisibilityMap[obs.SteamID] = map[uint64]bool{}
			}

			for j := range frameData.Players {
				tgt := frameData.Players[j]
				if obs.SteamID == tgt.SteamID || !tgt.IsAlive {
					continue
				}

				visible := p.rayVisible(obs, tgt)

				if visible {
					if _, ok := p.enemySpottedTime[obs.SteamID]; !ok {
						p.enemySpottedTime[obs.SteamID] = make(map[uint64]int)
					}

					if _, alreadySpotted := p.enemySpottedTime[obs.SteamID][tgt.SteamID]; !alreadySpotted {
						p.enemySpottedTime[obs.SteamID][tgt.SteamID] = p.currentTick
						if p.debug {
							fmt.Printf("[DEBUG] Player %d spotted %d at tick %d\n", obs.SteamID, tgt.SteamID, p.currentTick)
						}
					}
				}
				frameData.VisibilityMap[obs.SteamID][tgt.SteamID] = visible
			}
		}
	} else if p.debug {
		fmt.Printf("[DEBUG] BSPChecker not initialized; skipping visibility checks for tick %d\n", p.currentTick)
	}

	// Store frameData in p.frameStorage
	p.frameStorage.frames = append(p.frameStorage.frames, frameData)
}

func (p *Parser) InitializeParser(demoFile string, debug bool) error {
	// Load the BSP data for the map
	if err := p.loadBspData(debug); err != nil {
		return fmt.Errorf("failed to load BSP data: %w", err)
	}

	fmt.Println("[INFO] BSP data loaded successfully.")
	return nil
}

func (p *Parser) loadBspData(debug bool) error {
	_, _, mapName := p.extractMapName(debug) // Assuming you have a method to get the map name
	bspPath := fmt.Sprintf("maps/%s", mapName)

	// Initialize the BSP checker
	var err error
	p.BspChecker, err = NewBSPVisibilityChecker(bspPath) // Replace with your BSP loader logic
	if err != nil {
		return err
	}
	return nil
}

func (p *Parser) rayVisible(obs PlayerFrameData, tgt PlayerFrameData) bool {
	// Ensure BSP is initialized before performing visibility checks
	if p.BspChecker == nil {
		if p.debug && !p.warnedBspChecker {
			fmt.Println("[DEBUG] Warning: BSP file not loaded, skipping visibility checks in rayVisible.")
			p.warnedBspChecker = true
		}
		// Return false or allow fallback logic based on your use case
		return false
	}

	// Check visibility using BSP checker
	visible := p.BspChecker.IsVisible(obs.Position, tgt.Position)
	if p.debug {
		fmt.Printf("[DEBUG] rayVisible: from (%v) to (%v), bspChecker says: %v\n",
			obs.Position, tgt.Position, visible)
	}
	if !visible {
		return false
	}

	// Additional checks for smoke and flash
	if p.isLineInSmoke(obs.Position, tgt.Position) || p.isPlayerFlashed(obs.SteamID) {
		return false
	}

	// Basic distance check
	dist := tgt.Position.Sub(obs.Position).Norm()
	if dist > 2000 {
		return false
	}

	// Angle check
	angleToTarget := calcAngleBetween(obs.Position, tgt.Position)
	angleDiff := float32(math.Abs(float64(angleToTarget - obs.ViewAngleX)))

	// Handle wrap-around angles (e.g., 359 vs 0)
	if angleDiff > 180 {
		angleDiff = 360 - angleDiff
	}

	// Require target to be within ~60° FOV
	if angleDiff > 60 {
		return false
	}

	return true
}

func calcAngleBetween(from, to r3.Vector) float32 {
	deltaX := to.X - from.X
	deltaY := to.Y - from.Y

	angleRad := math.Atan2(float64(deltaY), float64(deltaX))
	angleDeg := angleRad * 180.0 / math.Pi
	return float32(angleDeg)
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

// handleActivity is primarily for utility damage, but keep it separate if you plan to expand it
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
	if !p.isLiveGameRound() || e.Shooter == nil || e.Shooter.SteamID64 == 0 {
		return
	}

	stats := p.match.GetOrCreatePlayerStats(e.Shooter.SteamID64, e.Shooter.Name)
	stats.ShotsTotal++

	shooterID := e.Shooter.SteamID64
	currentTick := p.parser.GameState().IngameTick()
	enemySpotted := false

	for _, enemy := range p.parser.GameState().Participants().Playing() {
		if enemy.Team == e.Shooter.Team || enemy.SteamID64 == 0 {
			continue
		}

		firstVisibleTick, found := p.findFirstVisibleTick(shooterID, enemy.SteamID64, currentTick, 64)

		// Debug info
		if p.debug {
			fmt.Printf("[DEBUG] Shooter: %s, Target: %s, Visible (within last 64 ticks): %v, EnemySpottedShots: %d\n",
				e.Shooter.Name, enemy.Name, found, stats.EnemySpottedShots)
		}

		if found {
			enemySpotted = true
			timeInView := float64(currentTick-firstVisibleTick) / 64.0 // Assuming ~64 ticks/sec
			stats.TimeToFirstShot = append(stats.TimeToFirstShot, timeInView)

			if p.debug {
				fmt.Printf("[DEBUG] Shooter: %s saw enemy for %.2f seconds before firing. Next SpottedShots => %d\n",
					e.Shooter.Name, timeInView, stats.EnemySpottedShots+1)
			}
		}
	}

	if enemySpotted {
		stats.EnemySpottedShots++
		if p.debug {
			fmt.Printf("[DEBUG] handleWeaponFire: %s => EnemySpottedShots incremented to %d\n",
				e.Shooter.Name, stats.EnemySpottedShots)
		}
	}

	// Track spray shots
	if p.frameStorage.frames != nil {
		stats.SprayShots++
	}

	// Track counter-strafing
	prevVel := stats.Velocity[e.Shooter.Name]
	currentVel := magnitude(e.Shooter.Velocity())
	if prevVel > 100 && currentVel < 20 {
		stats.CounterStrafedShots++
	}
	stats.Velocity[e.Shooter.Name] = currentVel

	// Update lastWeaponFireTime for trade logic
	p.lastWeaponFireTime[shooterID] = time.Now()
}

func (p *Parser) findFirstVisibleTick(attackerID, victimID uint64, currentTick, maxLookback int) (int, bool) {
	// Search backwards in frameStorage for up to maxLookback ticks
	for i := len(p.frameStorage.frames) - 1; i >= 0; i-- {
		fd := p.frameStorage.frames[i]
		if fd.Tick < currentTick-maxLookback {
			break
		}
		if visibleMap, ok := fd.VisibilityMap[attackerID]; ok {
			if visibleMap[victimID] {
				// Found a frame where attacker could see victim
				return fd.Tick, true
			}
		}
	}
	return -1, false
}

func (p *Parser) isLiveGameRound() bool {
	gs := p.parser.GameState()
	return !gs.IsWarmupPeriod() && gs.TotalRoundsPlayed() >= 0
}

func (p *Parser) handlePlayerHurt(e events.PlayerHurt) {
	if !p.isLiveGameRound() || e.Attacker == nil || e.Player == nil ||
		e.Attacker.SteamID64 == 0 || e.Player.SteamID64 == 0 {
		return
	}

	stats := p.match.GetOrCreatePlayerStats(e.Attacker.SteamID64, e.Attacker.Name)
	stats.HitsTotal++

	if e.HitGroup == events.HitGroupHead {
		stats.Headshots++
	}

	victimID := e.Player.SteamID64
	oldHP := p.lastKnownHP[victimID]
	actualDamage := min(e.HealthDamage, oldHP)

	// Count as total damage only if not team damage or self-inflicted
	if e.Attacker.Team != e.Player.Team && e.Attacker.SteamID64 != e.Player.SteamID64 {
		stats.TotalDamage += actualDamage
	}

	p.lastKnownHP[victimID] = max(oldHP-actualDamage, 0)

	// Check if victim was previously spotted
	if p.BspChecker != nil {
		if spottedTick, wasSpotted := p.enemySpottedTime[e.Attacker.SteamID64][victimID]; wasSpotted {
			// Count this as a spotted hit
			stats.EnemySpottedHits++
			if p.debug {
				fmt.Printf("[DEBUG] handlePlayerHurt: %s => EnemySpottedHits incremented to %d\n",
					e.Attacker.Name, stats.EnemySpottedHits)
			}

			// Track ticks to first damage
			if _, alreadyDamaged := p.firstDamageTime[e.Attacker.SteamID64][victimID]; !alreadyDamaged {
				ticksToHit := p.currentTick - spottedTick
				stats.TimeToFirstDamage = append(stats.TimeToFirstDamage, float64(ticksToHit))

				if _, exists := p.firstDamageTime[e.Attacker.SteamID64]; !exists {
					p.firstDamageTime[e.Attacker.SteamID64] = make(map[uint64]int)
				}
				p.firstDamageTime[e.Attacker.SteamID64][victimID] = p.currentTick

				if p.debug {
					fmt.Printf("[DEBUG] Attacker %s saw victim %s for %d ticks before hitting.\n",
						e.Attacker.Name, e.Player.Name, ticksToHit)
				}
			}
		}
	} else if p.debug && !p.warnedBspChecker {
		fmt.Println("[DEBUG] Warning: bspChecker is nil, skipping visibility checks for damage events.")
		p.warnedBspChecker = true
	}
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

func (p *Parser) isPlayerFlashed(playerID uint64) bool {
	if flashEndTime, exists := p.flashedPlayers[playerID]; exists {
		// If now is *before* flashEndTime, the player is still flashed
		return time.Now().Before(flashEndTime)
	}
	return false
}

func (p *Parser) updateSmokes(gameState dem.GameState) {
	// Clear old smokes
	p.smokePositions = nil

	// Get all active smoke positions
	for _, grenade := range gameState.GrenadeProjectiles() {
		if grenade.WeaponInstance.Type == common.EqSmoke {
			p.smokePositions = append(p.smokePositions, grenade.Position())
		}
	}
}

func (p *Parser) isLineInSmoke(start, end r3.Vector) bool {
	for _, smokePos := range p.smokePositions {
		// Simple smoke check - if line passes within smoke radius
		smokeRadius := 144.0

		distToLine := distancePointToLine(smokePos, start, end)
		if distToLine < smokeRadius {
			return true
		}
	}
	return false
}

func distancePointToLine(point, lineStart, lineEnd r3.Vector) float64 {
	// Calculate distance from point to line segment
	line := lineEnd.Sub(lineStart)
	length := line.Norm()
	if length == 0 {
		return point.Sub(lineStart).Norm()
	}

	t := point.Sub(lineStart).Dot(line) / (length * length)
	t = math.Max(0, math.Min(1, t))

	projection := lineStart.Add(line.Mul(t))
	return point.Sub(projection).Norm()
}

func (p *Parser) SetBSPChecker(bspChecker *BSPVisibilityChecker) {
	if bspChecker == nil {
		fmt.Println("[DEBUG] Warning: BSPChecker is nil.")
	} else {
		fmt.Println("[DEBUG] BSPChecker successfully initialized.")
	}
	p.BspChecker = bspChecker
}

func magnitude(v r3.Vector) float64 {
	return math.Sqrt(v.X*v.X + v.Y*v.Y + v.Z*v.Z)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
