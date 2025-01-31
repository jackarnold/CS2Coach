package parser

import (
	"errors"
	"fmt"
	"log"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/golang/geo/r3"
	dem "github.com/markus-wa/demoinfocs-golang/v4/pkg/demoinfocs"
	common "github.com/markus-wa/demoinfocs-golang/v4/pkg/demoinfocs/common"
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

type PlayerTickData struct {
	SteamID    uint64
	PlayerName string
	PlayerTeam common.Team
	Position   r3.Vector
	ViewAngleX float32
	ViewAngleY float32
	IsAlive    bool
	Velocity2D float64
	Velocity3D float64
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

type TickData struct {
	Tick    int
	Players []PlayerTickData
}

func (t TickData) String() string {
	var playerStrings []string
	for _, player := range t.Players {
		playerStrings = append(playerStrings, player.String())
	}
	return fmt.Sprintf("TickData{Tick: %d, Players: [%s]}", t.Tick, strings.Join(playerStrings, ", "))
}

func NewTickData(tick int) *TickData {
	return &TickData{
		Tick:    tick,
		Players: make([]PlayerTickData, 10),
	}
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
	timeDelta := c.tickTime.Seconds()

	displacement := currentPos.Sub(lastPos)
	return math.Sqrt(displacement.X*displacement.X+
		displacement.Y*displacement.Y+
		displacement.Z*displacement.Z) / timeDelta
}

func (c *Collector) calculateVelocity2D(currentPos, lastPos r3.Vector) float64 {
	timeDelta := c.tickTime.Seconds()

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

	for _, player := range gs.Participants().Playing() {
		if player.SteamID64 == 0 {
			continue
		}

		if _, exists := c.perTickInfo[currentTick][player.SteamID64]; !exists {
			c.perTickInfo[currentTick] = make(map[uint64]PlayerTickData)
		}

		var lastPlayerTick PlayerTickData
		if value, exists := c.perTickInfo[currentTick-1][player.SteamID64]; exists {
			lastPlayerTick = value
		}

		isAlive := player.IsAlive()
		velocity2D := float64(0)
		velocity3D := float64(0)

		if isAlive {
			velocity2D = c.calculateVelocity2D(player.Position(), lastPlayerTick.Position)
			velocity3D = c.calculateVelocity3D(player.Position(), lastPlayerTick.Position)
		}

		playerTick := PlayerTickData{
			SteamID:    player.SteamID64,
			PlayerTeam: player.Team,
			PlayerName: player.Name,
			Position:   player.Position(),
			ViewAngleX: player.ViewDirectionX(),
			ViewAngleY: player.ViewDirectionY(),
			IsAlive:    isAlive,
			Velocity2D: velocity2D,
			Velocity3D: velocity3D,
		}

		// This is fairly verbose, adjust as necessary
		if currentTick%10000 == 0 {
			c.logger.Debug("Adding playerTick", "currentTick", currentTick, "playerTick", playerTick)
		}

		c.perTickInfo[currentTick][player.SteamID64] = playerTick
	}
}

func (c *Collector) analyzeTimeToDamage() {
	for tick, playerData := range c.perTickInfo {
		for _, playerTick := range playerData {
			if !playerTick.IsAlive {
				continue // Ignore dead players
			}

			if tick%10000 == 0 {
				c.logger.Debug("Analyzing...",
					"tick", tick,
					"playerTick.PlayerName", playerTick.PlayerName,
					"playerTick.Velocity2D", playerTick.Velocity2D,
					"playerTick.Velocity3D", playerTick.Velocity3D,
				)
			}
			// Find the first tick where the player saw an enemy before firing
			/*if firstSightTick, exists := c.firstEnemySpottedTick(steamID, tick); exists {
				timeToDamage := (tick - firstSightTick) * (1000 / 64) // Convert ticks to ms

				if timeToDamage < 1000 { // Exclude trigger discipline cases (1s+)
					fmt.Printf("Player %d fired after %d ms (tick %d → %d)\n", steamID, timeToDamage, firstSightTick, tick)
				} else {
					fmt.Printf("Excluded trigger discipline for player %d (Time: %d ms, tick %d → %d)\n", steamID, timeToDamage, firstSightTick, tick)
				}
			}*/
		}
	}
}
