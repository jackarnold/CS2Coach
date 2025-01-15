package coach

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/richardkiene/CS2Coach/internal/models"
)

type Coach struct {
	ollamaURL string
}

type ollamaRequest struct {
	Model   string                 `json:"model"`
	Prompt  string                 `json:"prompt"`
	Stream  bool                   `json:"stream"`
	Options map[string]interface{} `json:"options"`
}

type ollamaResponse struct {
	Response   string `json:"response"`
	Context    []int  `json:"context,omitempty"`
	Created_at string `json:"created_at"`
	Model      string `json:"model"`
	Total_ms   int    `json:"total_ms"`
}

func NewCoach() *Coach {
	return &Coach{
		ollamaURL: "http://localhost:11434/api/generate",
	}
}

func (c *Coach) GetAdvice(stats *models.AnalyzedStats) (string, error) {
	fmt.Printf("Generating coaching advice...\n")

	reqBody := ollamaRequest{
		Model:  "llama3.2",
		Prompt: generatePrompt(stats),
		Stream: false,
		Options: map[string]interface{}{
			"temperature": 0.7,
			"top_p":       0.9,
		},
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("error marshaling request: %v", err)
	}

	fmt.Printf("Sending request to Ollama...\n")
	resp, err := http.Post(c.ollamaURL, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return "", fmt.Errorf("error making request to Ollama: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("error reading response: %v", err)
	}

	fmt.Printf("Response from Ollama: %s\n", string(body))

	var result ollamaResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("error unmarshaling response: %v", err)
	}

	return result.Response, nil
}

func generatePrompt(stats *models.AnalyzedStats) string {
	return fmt.Sprintf(`You are an expert CS2 coach analyzing a player's performance.

Player Statistics:
- Kills: %d
- Deaths: %d
- Assists: %d
- Headshots: %d
- Flash Assists: %d

Advanced Metrics:
- Kills per Round: %.2f
- Headshot %%: %.2f
- Accuracy: %.2f%%
- Opening Duel Success: %.2f%%
- Clutch Success: %.2f%%
- Utility Damage per Round: %.2f

Please provide:
1. Key strengths demonstrated in this match
2. Areas for improvement
3. Specific practice recommendations
4. Strategic adjustments for future matches
5. Utility usage optimization suggestions`,
		stats.BasicStats.Kills,
		stats.BasicStats.Deaths,
		stats.BasicStats.Assists,
		stats.BasicStats.Headshots,
		stats.BasicStats.FlashAssists,
		stats.AdvancedStats["KillsPerRound"],
		stats.AdvancedStats["HeadshotPercentage"],
		stats.AdvancedStats["Accuracy"],
		stats.AdvancedStats["OpeningDuelSuccess"],
		stats.AdvancedStats["ClutchSuccess"],
		stats.AdvancedStats["UtilityDamagePerRound"])
}
