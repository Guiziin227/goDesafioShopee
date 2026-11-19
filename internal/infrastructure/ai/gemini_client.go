package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/guiziin227/enriquecedor-catalogo/internal/domain"
)

type GeminiClient struct {
	apiKey     string
	model      string
	httpClient *http.Client
}

func NewGeminiClient(apiKey string) *GeminiClient {
	return &GeminiClient{
		apiKey:     apiKey,
		model:      "gemini-2.5-flash",
		httpClient: &http.Client{},
	}
}

// Estruturas internas para montar o payload da API do Gemini
type geminiRequest struct {
	Contents         []geminiContent        `json:"contents"`
	GenerationConfig geminiGenerationConfig `json:"generationConfig"`
}

type geminiContent struct {
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text string `json:"text"`
}

type geminiGenerationConfig struct {
	ResponseMimeType string         `json:"responseMimeType"`
	ResponseSchema   map[string]any `json:"responseSchema"`
	Temperature      float64        `json:"temperature"`
}

type geminiResponse struct {
	Candidates []struct {
		Content struct {
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
		} `json:"content"`
	} `json:"candidates"`
}

func (c *GeminiClient) EnrichProduct(ctx context.Context, product domain.Product) (*domain.EnrichmentData, error) {
	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s", c.model, c.apiKey)

	prompt := fmt.Sprintf(
		"Você é um especialista em catalogação de e-commerce da Shopee. Analise o produto abaixo e gere: "+
			"1) Uma categoria hierárquica em português (ex: 'Eletrônicos > Áudio > Fones'). "+
			"2) De 3 a 5 tags relevantes em minúsculo para busca. "+
			"3) Uma nota de qualidade do anúncio (quality_score) de 0.0 a 1.0 baseada na clareza do título e descrição.\n\n"+
			"Título: %s\nDescrição: %s\nPreço: R$ %.2f",
		product.Title, product.Description, product.Price,
	)

	reqPayload := geminiRequest{
		Contents: []geminiContent{
			{Parts: []geminiPart{{Text: prompt}}},
		},
		GenerationConfig: geminiGenerationConfig{
			ResponseMimeType: "application/json",
			Temperature:      0.2,
			ResponseSchema: map[string]any{
				"type": "OBJECT",
				"properties": map[string]any{
					"suggested_category": map[string]any{"type": "STRING"},
					"tags": map[string]any{
						"type":  "ARRAY",
						"items": map[string]any{"type": "STRING"},
					},
					"quality_score": map[string]any{"type": "NUMBER"},
				},
				"required": []string{"suggested_category", "tags", "quality_score"},
			},
		},
	}

	bodyBytes, err := json.Marshal(reqPayload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal gemini request: %w", err)
	}

	// http.NewRequestWithContext garante que se o context atingir o timeout de 2s (ou 5s para LLM),
	// a conexão TCP é abortada imediatamente.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBuffer(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create http request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gemini api request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("gemini api returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var gResp geminiResponse
	if err := json.NewDecoder(resp.Body).Decode(&gResp); err != nil {
		return nil, fmt.Errorf("failed to decode gemini response: %w", err)
	}

	if len(gResp.Candidates) == 0 || len(gResp.Candidates[0].Content.Parts) == 0 {
		return nil, domain.ErrAIServiceFailure
	}

	rawJSON := gResp.Candidates[0].Content.Parts[0].Text

	var enrichment domain.EnrichmentData
	if err := json.Unmarshal([]byte(rawJSON), &enrichment); err != nil {
		return nil, fmt.Errorf("failed to parse structured output json: %w", err)
	}

	return &enrichment, nil
}
