package parser

import (
	"errors"
	"fmt"
	"log"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"time"

	"github.com/golang/geo/r3"
	dem "github.com/markus-wa/demoinfocs-golang/v4/pkg/demoinfocs"
	common "github.com/markus-wa/demoinfocs-golang/v4/pkg/demoinfocs/common"
	"github.com/markus-wa/demoinfocs-golang/v4/pkg/demoinfocs/events"
	"github.com/markus-wa/demoinfocs-golang/v4/pkg/demoinfocs/msgs2"
	"github.com/richardkiene/CS2Coach/internal/models"
)

type Collector struct {
	tickRate     float64
	tickTime     time.Duration
	mapNameFound bool
	match        *models.Match
	bspChecker   *BSPVisibilityChecker
	parser       dem.Parser
	logger       slog.Logger
	perTickInfo  map[int]map[uint64]PlayerTickData
}

type DamageDealt struct {
	ArmorDamage  int
	HealthDamage int
	HitGroup     byte
}

type PlayerTickData struct {
	SteamID                uint64
	PlayerName             string
	PlayerTeam             common.Team
	Position               r3.Vector
	ViewAngleX             float32
	ViewAngleY             float32
	IsAlive                bool
	Velocity2D             float64
	Velocity3D             float64
	ActiveWeapon           *common.Equipment
	AmmoLeft               [32]int
	EntityID               int
	FlashedAtTick          int
	FlashedTimeRemaining   time.Duration
	Team                   common.Team
	IsAirborne             bool
	IsBlinded              bool
	IsCrouched             bool
	IsConnected            bool
	IsBot                  bool
	IsDefusing             bool
	IsPlanting             bool
	IsReloading            bool
	IsScoped               bool
	IsUpright              bool
	IsWalking              bool
	HasHelmet              bool
	HasKit                 bool
	FiredActiveWeapon      bool
	ArmorRemaining         int
	Assists                int
	Deaths                 int
	Kills                  int
	Health                 int
	Armor                  int
	Damage                 int
	UtilityDamage          int
	Money                  int
	CurrentRoundMoneySpent int
	CurrentMoneySpentTotal int
	DamageDealtToPlayer    map[uint64]DamageDealt
}

func (p *PlayerTickData) ForwardVector() r3.Vector {
	// Convert degrees to radians
	yaw := float64(p.ViewAngleX) * (math.Pi / 180)
	pitch := float64(p.ViewAngleY) * (math.Pi / 180)

	// Compute the forward vector components
	forward := r3.Vector{
		X: math.Cos(pitch) * math.Cos(yaw),
		Y: math.Cos(pitch) * math.Sin(yaw),
		Z: -math.Sin(pitch), // Negative because up is usually negative in CS2
	}

	return forward.Normalize() // Ensure it's a unit vector
}

func (p PlayerTickData) String() string {
	var team string

	if p.PlayerTeam == 2 {
		team = "Terrorists"
	} else if p.PlayerTeam == 3 {
		team = "Counter-Terrorists"
	} else {
		team = "Unknown"
	}

	return fmt.Sprintf("PlayerTickData{SteamID: %d, Name: %s, Team: %s, Pos: (%.2f, %.2f, %.2f), ViewAngle: (%.2f, %.2f), Alive: %t, Vel2D: %.2f, Vel3D: %.2f}",
		p.SteamID, p.PlayerName, team,
		p.Position.X, p.Position.Y, p.Position.Z,
		p.ViewAngleX, p.ViewAngleY, p.IsAlive,
		p.Velocity2D, p.Velocity3D,
	)
}

func NewCollector(logger *slog.Logger) *Collector {
	return &Collector{
		match:       models.NewMatch(),
		logger:      *logger,
		tickRate:    -1,
		tickTime:    -1,
		perTickInfo: make(map[int]map[uint64]PlayerTickData, 0),
	}
}

func (c *Collector) Collect(demoPath string) (*models.Match, error) {
	f, err := os.Open(demoPath)
	if err != nil {
		return nil, err
	}

	defer f.Close()

	c.parser = dem.NewParser(f)
	defer c.parser.Close()

	c.registerEventHandlers()
	cs2MapsPath, err := c.determineCS2MapsPath()

	if err != nil {
		return nil, err
	}

	// Parse frames until we handle a ServerInfo event with the map name available
	// https://github.com/markus-wa/demoinfocs-golang/issues/435#issuecomment-2613140840
	for !c.mapNameFound {
		moreFrames, err := c.parser.ParseNextFrame()
		if err != nil || !moreFrames {
			if err == dem.ErrUnexpectedEndOfDemo {
				return nil, fmt.Errorf("unable to determine map name from demo file")
			}
			return nil, fmt.Errorf("error during initial parsing: %v", err)
		}
	}

	c.logger.Info("Map name detected: ", "MapName", c.match.MapName)

	bspChecker, err := NewBSPVisibilityChecker(c.match.MapName, cs2MapsPath, c.logger)
	if err != nil {
		log.Fatalf("Failed to load BSP data for map %s: %v\n", c.match.MapName, err)
	} else {
		c.bspChecker = bspChecker
		c.logger.Debug("Successfully loaded BSP data", "MapName", c.match.MapName)
	}

	c.logger.Debug("Resuming full parsing...")

	if err := c.parser.ParseToEnd(); err != nil {
		return nil, fmt.Errorf("error during full parsing: %v", err)
	}

	c.logger.Debug("Finished parsing events", "Events", len(c.match.Events))

	for steamID, stats := range c.match.PlayerStats {
		c.logger.Info("Player stats",
			"name", stats.Name,
			"steam_id", steamID,
			"kills", stats.Kills,
			"deaths", stats.Deaths,
			"assists", stats.Assists,
			"total_damage", stats.TotalDamage,
		)
	}

	return c.match, nil
}

func (c *Collector) registerEventHandlers() {
	c.parser.RegisterNetMessageHandler(c.handleServerInfo)
	c.parser.RegisterNetMessageHandler(c.handleEntityUpdate)
	c.parser.RegisterEventHandler(c.handleWeaponFire)
	c.parser.RegisterEventHandler(c.handlePlayerHurt)
}

func (c *Collector) determineCS2MapsPath() (string, error) {
	paths := []string{
		`C:\Program Files (x86)\Steam\steamapps\common\Counter-Strike Global Offensive\game\csgo`,
		`C:\Program Files\Steam\steamapps\common\Counter-Strike Global Offensive\game\csgo`,
	}

	for _, path := range paths {
		if fileExists(filepath.Join(path, "maps")) {
			return path, nil
		}
	}

	return "", errors.New("path not found")

}

func (c *Collector) calculateVelocity3D(currentPos, lastPos r3.Vector) float64 {
	timeDelta := float64(c.tickTime.Milliseconds())

	displacement := currentPos.Sub(lastPos)
	return math.Sqrt(displacement.X*displacement.X+
		displacement.Y*displacement.Y+
		displacement.Z*displacement.Z) / timeDelta
}

func (c *Collector) calculateVelocity2D(currentPos, lastPos r3.Vector) float64 {
	timeDelta := float64(c.tickTime.Milliseconds())

	displacement := currentPos.Sub(lastPos)
	return math.Sqrt(displacement.X*displacement.X+
		displacement.Y*displacement.Y) / timeDelta
}

func (c *Collector) handleServerInfo(msg *msgs2.CSVCMsg_ServerInfo) {
	mapName := msg.GetMapName()
	if !c.mapNameFound && mapName != "" {
		c.match.MapName = mapName
		c.mapNameFound = true
		c.logger.Debug("Map name detected from server info", "MapName", c.match.MapName)
	}
}

func (c *Collector) handleEntityUpdate(msg *msgs2.CSVCMsg_PacketEntities) {
	if c.tickRate == -1 {
		c.tickRate = c.parser.TickRate()
		c.tickTime = c.parser.TickTime()

		c.logger.Debug("Server Tickrate", "tickRate", c.tickRate)
		c.logger.Debug("Tick time", "tickTime", c.tickTime)
	}

	// If we haven't yet found the map name and loaded the visibility files don't do work.
	// TODO: We probably want to make sure this doesn't continue so perhaps we check currentTick, too.
	if c.bspChecker == nil {
		return
	}

	gs := c.parser.GameState()
	currentTick := gs.IngameTick()

	// At the beginning of the demo, IngameTick can be invalid
	if currentTick < 0 {
		return
	}

	if c.perTickInfo[currentTick] == nil {
		c.perTickInfo[currentTick] = make(map[uint64]PlayerTickData)
	}

	prevTick := currentTick - 1
	var previousTickMap map[uint64]PlayerTickData
	if prevTick >= 0 {
		previousTickMap = c.perTickInfo[prevTick]
	}

	for _, player := range gs.Participants().Playing() {
		if player.SteamID64 == 0 {
			continue
		}

		var lastPlayerTick PlayerTickData
		if previousTickMap != nil {
			if ptd, ok := previousTickMap[player.SteamID64]; ok {
				lastPlayerTick = ptd
			}
		}

		var velocity2D, velocity3D float64

		// We only want to calculate the velocity of a player if they are alive
		if player.IsAlive() {
			velocity2D = c.calculateVelocity2D(player.Position(), lastPlayerTick.Position)
			velocity3D = c.calculateVelocity3D(player.Position(), lastPlayerTick.Position)
		}

		playerTick, found := c.perTickInfo[currentTick][player.SteamID64]
		if !found {
			// Only create a new struct if this is the first time we see this player on this tick
			playerTick = PlayerTickData{
				SteamID:             player.SteamID64,
				DamageDealtToPlayer: make(map[uint64]DamageDealt),
			}
		}

		playerTick.SteamID = player.SteamID64
		playerTick.PlayerTeam = player.Team
		playerTick.PlayerName = player.Name
		playerTick.Position = player.Position()
		playerTick.ViewAngleX = player.ViewDirectionX()
		playerTick.ViewAngleY = player.ViewDirectionY()
		playerTick.IsAlive = player.IsAlive()
		playerTick.Velocity2D = velocity2D
		playerTick.Velocity3D = velocity3D
		playerTick.ActiveWeapon = player.ActiveWeapon()
		playerTick.AmmoLeft = player.AmmoLeft
		playerTick.EntityID = player.Entity.ID()
		playerTick.FlashedAtTick = player.FlashTick
		playerTick.FlashedTimeRemaining = player.FlashDurationTimeRemaining()
		playerTick.Team = player.Team
		playerTick.IsConnected = player.IsConnected
		playerTick.IsAirborne = player.IsAirborne()
		playerTick.IsBlinded = player.IsBlinded()
		playerTick.IsBot = player.IsBot
		playerTick.IsCrouched = player.IsDucking()
		playerTick.IsDefusing = player.IsDefusing
		playerTick.IsPlanting = player.IsPlanting
		playerTick.IsReloading = player.IsReloading
		playerTick.IsScoped = player.IsScoped()
		playerTick.IsUpright = player.IsStanding()
		playerTick.IsWalking = player.IsWalking()
		playerTick.Assists = player.Assists()
		playerTick.Deaths = player.Deaths()
		playerTick.Kills = player.Kills()
		playerTick.Damage = player.TotalDamage()
		playerTick.Health = player.Health()
		playerTick.Armor = player.Armor()
		playerTick.UtilityDamage = player.UtilityDamage()
		playerTick.Money = player.Money()
		playerTick.CurrentRoundMoneySpent = player.MoneySpentThisRound()
		playerTick.CurrentMoneySpentTotal = player.MoneySpentTotal()

		c.perTickInfo[currentTick][player.SteamID64] = playerTick
	}
}

func (c *Collector) handleWeaponFire(e events.WeaponFire) {
	if e.Shooter == nil {
		return
	}

	currentTick := c.parser.GameState().IngameTick()

	if _, exists := c.perTickInfo[currentTick][e.Shooter.SteamID64]; !exists {
		c.perTickInfo[currentTick] = make(map[uint64]PlayerTickData)
	}

	shooterData := c.perTickInfo[currentTick][e.Shooter.SteamID64]
	shooterData.FiredActiveWeapon = true
	c.perTickInfo[currentTick][e.Shooter.SteamID64] = shooterData
}

func (c *Collector) handlePlayerHurt(e events.PlayerHurt) {
	gs := c.parser.GameState()
	currentTick := gs.IngameTick()

	if e.Attacker == nil || e.Player == nil {
		return
	}

	attackerID := e.Attacker.SteamID64
	victimID := e.Player.SteamID64

	if _, exists := c.perTickInfo[currentTick][attackerID]; !exists {
		c.perTickInfo[currentTick] = make(map[uint64]PlayerTickData)
	}

	if _, exists := c.perTickInfo[currentTick][victimID]; !exists {
		c.perTickInfo[currentTick] = make(map[uint64]PlayerTickData)
	}

	attackerData := c.perTickInfo[currentTick][attackerID]
	victimData := c.perTickInfo[currentTick][victimID]

	if attackerData.DamageDealtToPlayer == nil {
		attackerData.DamageDealtToPlayer = make(map[uint64]DamageDealt)
	}

	currentDamage := DamageDealt{
		ArmorDamage:  e.ArmorDamageTaken,
		HealthDamage: e.HealthDamageTaken,
		HitGroup:     byte(e.HitGroup),
	}

	attackerData.DamageDealtToPlayer[victimID] = currentDamage

	c.perTickInfo[currentTick][attackerID] = attackerData
	c.perTickInfo[currentTick][victimID] = victimData
}

func (c *Collector) AnalyzeTimeToDamage() {
	c.logger.Debug("Starting AnalyzeTimeToDamage")
	c.logger.Debug("perTickInfo state", "numTicks", len(c.perTickInfo))

	for tick, playerData := range c.perTickInfo {
		for steamID, playerTick := range playerData {
			if !playerTick.IsAlive || playerTick.DamageDealtToPlayer == nil {
				continue
			}

			for victimID, damage := range playerTick.DamageDealtToPlayer {
				if damage.HealthDamage > 0 {
					if playerTick.PlayerName == "shmeeny" {
						c.logger.Debug("DamageDealt",
							"tick", tick,
							"PlayerName", playerTick.PlayerName,
							"PlayerID", steamID,
							"VictimName", c.perTickInfo[tick][victimID].PlayerName,
							"VictimID", victimID,
							"Damage", damage.HealthDamage,
						)
					}
					firstSightTick, exists := c.findLastContinuousVisibilityStart(steamID, victimID, tick)
					if !exists {
						continue
					}

					timeToDamage := int64(tick-firstSightTick) * c.tickTime.Milliseconds()

					if timeToDamage < 0 {
						if playerTick.PlayerName == "shmeeny" {
							continue
						}
					}

					if timeToDamage > 0 && timeToDamage < 1000 {
						if playerTick.PlayerName == "shmeeny" {
							c.logger.Warn("Player time to damage",
								"player", playerTick.PlayerName,
								"steamID", steamID,
								"reactionTimeMs", timeToDamage,
								"damageAmount", damage.HealthDamage)
						}
					} else {
						continue
					}
				}
			}
		}
	}
}

func (c *Collector) findLastContinuousVisibilityStart(playerID, targetID uint64, currentTick int) (int, bool) {
	firstSeenTick := -1
	lastSeenTick := -1
	lostVisibilityTick := -1

	for tick := currentTick; tick >= 0; tick-- {
		playerData, exists := c.perTickInfo[tick]
		if !exists {
			continue
		}

		playerTick, exists := playerData[playerID]
		if !exists || !playerTick.IsAlive || playerTick.IsBlinded {
			continue
		}

		targetTick, exists := playerData[targetID]
		if !exists || !targetTick.IsAlive {
			continue
		}

		// EVIL TESTING HACK -- REMOVE ME
		if playerTick.SteamID != 76561197991944713 {
			continue
		}
		// END EVIL TESTING HACK -- REMOVE ME

		isVisible := c.bspChecker.IsVisible(playerTick, targetTick)

		/*c.logger.Debug("findLastContinuousVisibilityStart -- Visibility check",
			"tick", tick,
			"player", playerTick.PlayerName,
			"target", targetTick.PlayerName,
			"isVisible", isVisible,
			"forwardVector", playerTick.ForwardVector(),
			"playerPos", playerTick.Position,
			"targetPos", targetTick.Position,
		)*/

		if isVisible {
			if lastSeenTick == -1 { // First tick of seeing the target
				lastSeenTick = tick
			}
			firstSeenTick = tick    // Keep updating first seen tick
			lostVisibilityTick = -1 // Reset lost visibility tracking
		} else {
			if lostVisibilityTick == -1 { // First tick visibility was lost
				lostVisibilityTick = tick
			}
			if lastSeenTick != -1 { // Stop once we find a period where they were seen
				break
			}
		}

		// Ensure we do not force firstSeenTick to be only within the last 128 ticks
		if tick == 0 && firstSeenTick != -1 {
			return firstSeenTick, true
		}
	}

	if firstSeenTick != -1 {
		return firstSeenTick, true
	}

	return 0, false
}
