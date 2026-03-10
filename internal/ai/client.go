package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/robobee/core/internal/config"
)

type Client struct {
	baseURL    string
	apiKey     string
	model      string
	httpClient *http.Client
}

func NewClient(cfg config.AIConfig) *Client {
	return &Client{
		baseURL: cfg.BaseURL,
		apiKey:  cfg.APIKey,
		model:   cfg.Model,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

func (c *Client) CronFromDescription(ctx context.Context, description string) (string, error) {
	reqBody := chatRequest{
		Model: c.model,
		Messages: []chatMessage{
			{
				Role:    "system",
				Content: "You are a cron expression generator. Convert the schedule description to a valid 5-field cron expression (minute hour day month weekday). Return ONLY the cron expression, nothing else. No explanations, no markdown.",
			},
			{
				Role:    "user",
				Content: description,
			},
		},
	}

	data, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("AI service returned status %d", resp.StatusCode)
	}

	var chatResp chatResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&chatResp); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}

	if len(chatResp.Choices) == 0 {
		return "", fmt.Errorf("AI service returned no choices")
	}

	cron := strings.TrimSpace(chatResp.Choices[0].Message.Content)
	if cron == "" {
		return "", fmt.Errorf("AI service returned empty cron expression")
	}

	return cron, nil
}

// WorkerSummary is used for AI routing decisions.
type WorkerSummary struct {
	ID          string
	Name        string
	Description string
}

// RouteToWorker uses AI to select the most appropriate worker for a message.
// Returns an error if the AI response does not match any worker ID in the list.
func (c *Client) RouteToWorker(ctx context.Context, message string, workers []WorkerSummary) (string, error) {
	var workerList strings.Builder
	validIDs := make(map[string]bool, len(workers))
	for _, w := range workers {
		fmt.Fprintf(&workerList, "- ID: %s, Name: %s, Description: %s\n", w.ID, w.Name, w.Description)
		validIDs[w.ID] = true
	}

	reqBody := chatRequest{
		Model: c.model,
		Messages: []chatMessage{
			{
				Role:    "system",
				Content: "You are a task router. Given a list of workers and a user message, return ONLY the ID of the most suitable worker. No explanation, no markdown, just the ID.",
			},
			{
				Role:    "user",
				Content: fmt.Sprintf("Workers:\n%s\nUser message: %s", workerList.String(), message),
			},
		},
	}

	data, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("AI service returned status %d", resp.StatusCode)
	}

	var chatResp chatResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&chatResp); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}

	if len(chatResp.Choices) == 0 {
		return "", fmt.Errorf("AI service returned no choices")
	}

	workerID := strings.TrimSpace(chatResp.Choices[0].Message.Content)
	if !validIDs[workerID] {
		return "", fmt.Errorf("AI returned unknown worker ID %q", workerID)
	}

	return workerID, nil
}
