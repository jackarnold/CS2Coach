package parser

import (
	"os"

	dem "github.com/markus-wa/demoinfocs-golang/v4/pkg/demoinfocs"
	events "github.com/markus-wa/demoinfocs-golang/v4/pkg/demoinfocs/events"
	"github.com/richardkiene/CS2Coach/internal/models"
)

type Parser struct {
	match *models.Match
}

func NewParser() *Parser {
	return &Parser{
		match: models.NewMatch(),
	}
}

func (p *Parser) ParseDemo(path string) (*models.Match, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	parser := dem.NewParser(f)
	defer parser.Close()

	// Register event handlers
	parser.RegisterEventHandler(func(e events.Kill) {
		p.handleKill(e)
	})

	parser.RegisterEventHandler(func(e events.WeaponFire) {
		p.handleWeaponFire(e)
	})

	parser.RegisterEventHandler(func(e events.PlayerHurt) {
		p.handlePlayerHurt(e)
	})

	parser.RegisterEventHandler(func(e events.RoundStart) {
		p.handleRoundStart(e)
	})

	parser.RegisterEventHandler(func(e events.RoundEnd) {
		p.handleRoundEnd(e)
	})

	// Parse to end
	err = parser.ParseToEnd()
	if err != nil {
		return nil, err
	}

	return p.match, nil
}

func (p *Parser) handleKill(e events.Kill) {
	killer := e.Killer
	victim := e.Victim
	if killer == nil || victim == nil {
		return
	}

	killerStats := p.match.GetOrCreatePlayerStats(killer.SteamID64, killer.Name)
	victimStats := p.match.GetOrCreatePlayerStats(victim.SteamID64, victim.Name)

	killerStats.Kills++
	victimStats.Deaths++

	if e.IsHeadshot {
		killerStats.Headshots++
	}

	p.match.AddEvent(models.Event{
		Type: "kill",
		Data: map[string]interface{}{
			"killer":   killer.Name,
			"victim":   victim.Name,
			"weapon":   e.Weapon.String(),
			"headshot": e.IsHeadshot,
		},
	})
}

func (p *Parser) handleWeaponFire(e events.WeaponFire) {
	shooter := e.Shooter
	if shooter == nil {
		return
	}

	stats := p.match.GetOrCreatePlayerStats(shooter.SteamID64, shooter.Name)
	stats.ShotsTotal++

	p.match.AddEvent(models.Event{
		Type: "weapon_fire",
		Data: map[string]interface{}{
			"player": shooter.Name,
			"weapon": e.Weapon.String(),
		},
	})
}

func (p *Parser) handlePlayerHurt(e events.PlayerHurt) {
	attacker := e.Attacker
	player := e.Player
	if attacker == nil || player == nil {
		return
	}

	stats := p.match.GetOrCreatePlayerStats(attacker.SteamID64, attacker.Name)
	stats.HitsTotal++

	p.match.AddEvent(models.Event{
		Type: "player_hurt",
		Data: map[string]interface{}{
			"attacker": attacker.Name,
			"victim":   player.Name,
			"weapon":   e.Weapon.String(),
			"damage":   e.HealthDamage,
			"armor":    e.ArmorDamage,
		},
	})
}

func (p *Parser) handleRoundStart(e events.RoundStart) {
	p.match.AddEvent(models.Event{
		Type: "round_start",
		Data: map[string]interface{}{},
	})
}

func (p *Parser) handleRoundEnd(e events.RoundEnd) {
	p.match.AddEvent(models.Event{
		Type: "round_end",
		Data: map[string]interface{}{
			"winner": e.Winner,
			"reason": e.Reason,
		},
	})
}
