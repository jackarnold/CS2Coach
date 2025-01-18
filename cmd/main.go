package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/richardkiene/CS2Coach/internal/analyzer"
	"github.com/richardkiene/CS2Coach/internal/coach"
	"github.com/richardkiene/CS2Coach/internal/ml"
	"github.com/richardkiene/CS2Coach/internal/models"
	"github.com/richardkiene/CS2Coach/internal/parser"
)

func main() {
	analyzeCmd := flag.NewFlagSet("analyze", flag.ExitOnError)
	trainCmd := flag.NewFlagSet("train", flag.ExitOnError)
	predictCmd := flag.NewFlagSet("predict", flag.ExitOnError)

	// Analyze command flags
	analyzeDemoPath := analyzeCmd.String("demo", "", "Path to CS2 demo file")
	analyzePlayerName := analyzeCmd.String("player", "", "Player name to analyze")
	analyzeSteamID := analyzeCmd.String("steamid", "", "Steam ID to analyze")
	analyzeDebug := analyzeCmd.Bool("debug", false, "Enable debug output")
	analyzeVerbose := analyzeCmd.Bool("verbose", false, "Enable verbose output")

	// Train command flags
	trainDemoDir := trainCmd.String("demodir", "", "Directory containing demo files for training")
	trainConfigPath := trainCmd.String("config", "", "Path to model configuration file")

	// Predict command flags
	predictDemoPath := predictCmd.String("demo", "", "Path to CS2 demo file")
	predictPlayerName := predictCmd.String("player", "", "Player name to predict")
	predictSteamID := predictCmd.String("steamid", "", "Steam ID to predict")

	if len(os.Args) < 2 {
		fmt.Println("Expected 'analyze', 'train', or 'predict' subcommands")
		os.Exit(1)
	}

	switch os.Args[1] {
	case "analyze":
		analyzeCmd.Parse(os.Args[2:])
		handleAnalyze(*analyzeDemoPath, *analyzePlayerName, *analyzeSteamID, *analyzeDebug, *analyzeVerbose)
	case "train":
		trainCmd.Parse(os.Args[2:])
		handleTrain(*trainDemoDir, *trainConfigPath, true, true)
	case "predict":
		predictCmd.Parse(os.Args[2:])
		handlePredict(*predictDemoPath, *predictPlayerName, *predictSteamID, true, true)
	default:
		fmt.Printf("%q is not valid command.\n", os.Args[1])
		os.Exit(1)
	}
}

func handleAnalyze(demoPath, playerName, steamID string, debug, verbose bool) {
	if demoPath == "" {
		log.Fatal("Please provide a demo file path")
	}
	if playerName == "" && steamID == "" {
		log.Fatal("Please provide either player name or Steam ID")
	}

	p := parser.NewParser()
	fmt.Printf("Parsing demo file: %s\n", demoPath)
	match, err := p.ParseDemo(demoPath, debug)
	if err != nil {
		log.Fatalf("Error parsing demo: %v", err)
	}

	a := analyzer.NewAnalyzer()
	stats := a.AnalyzeMatch(match, playerName, steamID, verbose)
	if stats == nil {
		log.Fatal("Player not found in demo")
	}

	c := coach.NewCoach()
	advice, err := c.GetAdvice(stats)
	if err != nil {
		log.Fatalf("Error getting coaching advice: %v", err)
	}

	fmt.Println(advice)
}

func handleTrain(demoDir, configPath string, debug bool, verbose bool) {
	if demoDir == "" {
		log.Fatal("Please provide a demo directory path")
	}

	// Initialize model
	model, err := ml.NewModel(configPath)
	if err != nil {
		log.Fatalf("Error creating model: %v", err)
	}

	// Collect demo files
	var matches []*models.Match
	err = filepath.Walk(demoDir, func(path string, info os.FileInfo, err error) error {
		if filepath.Ext(path) == ".dem" {
			p := parser.NewParser()
			match, err := p.ParseDemo(path, debug)
			if err != nil {
				fmt.Printf("Warning: Error parsing demo %s: %v\n", path, err)
				return nil
			}
			matches = append(matches, match)
		}
		return nil
	})

	if err != nil {
		log.Fatalf("Error walking demo directory: %v", err)
	}

	fmt.Printf("Training model on %d demos...\n", len(matches))
	if err := model.Train(matches); err != nil {
		log.Fatalf("Error training model: %v", err)
	}

	fmt.Println("Model training completed successfully")
}

func handlePredict(demoPath, playerName, steamID string, debug bool, verbose bool) {
	if demoPath == "" {
		log.Fatal("Please provide a demo file path")
	}
	if playerName == "" && steamID == "" {
		log.Fatal("Please provide either player name or Steam ID")
	}

	// Parse demo
	p := parser.NewParser()
	match, err := p.ParseDemo(demoPath, debug)
	if err != nil {
		log.Fatalf("Error parsing demo: %v", err)
	}

	// Analyze match
	a := analyzer.NewAnalyzer()
	stats := a.AnalyzeMatch(match, playerName, steamID, verbose)
	if stats == nil {
		log.Fatal("Player not found in demo")
	}

	// Initialize model and predict
	model, err := ml.NewModel("")
	if err != nil {
		log.Fatalf("Error creating model: %v", err)
	}

	prediction, err := model.Predict(stats)
	if err != nil {
		log.Fatalf("Error making prediction: %v", err)
	}

	fmt.Printf("\nPrediction Results:\n")
	fmt.Printf("Player Impact: %s (%.2f%% confidence)\n",
		prediction.Outcome, prediction.Probability*100)
}
