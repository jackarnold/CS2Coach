package coach

import "fmt"

// GetAnalysisPrompt generates a detailed prompt for Ollama analysis
func GetAnalysisPrompt(playerName string, impactScore, utilityScore, aimScore float64) string {
	return fmt.Sprintf(`As an expert CS2 coach, analyze %s's performance:

Performance Metrics:
- Impact Score: %.1f/100 (Opening duels, clutches, multi-kills)
- Utility Score: %.1f/100 (Grenade damage, flash assists)
- Aim Score: %.1f/100 (Accuracy, headshot rate)

Provide specific feedback on:
1. Current strengths and standout performance areas
2. Key weaknesses and areas for immediate improvement
3. Recommended practice routines and workshop maps
4. Strategic adjustments for future matches
5. Utility usage optimization
`, playerName, impactScore, utilityScore, aimScore)
}

// GetTrainingPrompt generates a prompt for suggesting training routines
func GetTrainingPrompt(aimScore float64, utilityScore float64) string {
	return fmt.Sprintf(`Based on the player's performance metrics:
- Aim Score: %.1f/100
- Utility Score: %.1f/100

Recommend a detailed training routine including:
1. Aim training exercises and workshop maps
2. Utility practice routines
3. Time allocation for different training aspects
4. Specific drills to focus on
5. Progress tracking metrics
`, aimScore, utilityScore)
}
