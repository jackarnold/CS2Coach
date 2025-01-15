package main

import (
	"flag"
	"fmt"
	"log"

	"github.com/richardkiene/CS2Coach/internal/analyzer"
	"github.com/richardkiene/CS2Coach/internal/coach"
	"github.com/richardkiene/CS2Coach/internal/parser"
)

func main() {
	demoPath := flag.String("demo", "", "Path to CS2 demo file")
	playerName := flag.String("player", "", "Player name to analyze")
	steamID := flag.String("steamid", "", "Steam ID to analyze")
	flag.Parse()

	if *demoPath == "" {
		log.Fatal("Please provide a demo file path")
	}

	if *playerName == "" && *steamID == "" {
		log.Fatal("Please provide either player name or Steam ID")
	}

	// Parse demo
	p := parser.NewParser()
	fmt.Printf("Parsing demo file: %s\n", *demoPath)
	match, err := p.ParseDemo(*demoPath)
	if err != nil {
		log.Fatalf("Error parsing demo: %v", err)
	}
	fmt.Printf("Found %d players\n", len(match.PlayerStats))

	// Print available players
	fmt.Println("\nAvailable players:")
	for _, stats := range match.PlayerStats {
		fmt.Printf("- %s (Steam ID: %d)\n", stats.Name, stats.SteamID)
	}

	// Analyze match
	a := analyzer.NewAnalyzer()
	stats := a.AnalyzeMatch(match, *playerName, *steamID)

	// Get coaching advice
	c := coach.NewCoach()
	advice, err := c.GetAdvice(stats)
	if err != nil {
		log.Fatalf("Error getting coaching advice: %v", err)
	}

	fmt.Println(advice)
}
