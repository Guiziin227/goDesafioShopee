package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	httpDelivery "github.com/guiziin227/enriquecedor-catalogo/internal/delivery/http"
	"github.com/guiziin227/enriquecedor-catalogo/internal/domain"
	"github.com/guiziin227/enriquecedor-catalogo/internal/infrastructure/ai"
	"github.com/guiziin227/enriquecedor-catalogo/internal/infrastructure/repository"
	"github.com/guiziin227/enriquecedor-catalogo/internal/usecase"
)

func main() {
	loadEnvFile(".env")

	// 1. Configurar Logger Estruturado em JSON
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	// 2. Ler configurações e decidir provedor de IA (Gemini Real vs Mock)
	port := getEnv("PORT", "8080")
	geminiKey := os.Getenv("GEMINI_API_KEY")

	var aiClient domain.AIClient
	defaultTimeoutSec := 2

	if geminiKey != "" {
		logger.Info("GEMINI_API_KEY detected: initializing real Gemini 2.5 Flash client")
		aiClient = ai.NewGeminiClient(geminiKey)
		defaultTimeoutSec = 5 // LLMs reais podem levar entre 1s e 4s
	} else {
		logger.Warn("GEMINI_API_KEY not set: falling back to MockAIClient")
		aiClient = ai.NewMockAIClient()
	}

	timeoutSec := getEnvAsInt("AI_TIMEOUT_SECONDS", defaultTimeoutSec)

	// 3. Inicializar Dependências (Repository -> UseCase -> Delivery)
	repo := repository.NewInMemoryBatchRepository()
	enricher := usecase.NewCatalogEnricher(repo, aiClient, logger, usecase.EnricherConfig{
		WorkerCount: 5,
		AITimeout:   time.Duration(timeoutSec) * time.Second,
	})

	// Contexto raiz cancelável para controlar o ciclo de vida dos workers
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Inicia o Worker Pool em background
	enricher.Start(ctx)

	// Configura rotas HTTP
	mux := http.NewServeMux()
	handler := httpDelivery.NewBatchHandler(enricher)
	handler.RegisterRoutes(mux)

	server := &http.Server{
		Addr:         ":" + port,
		Handler:      httpDelivery.LoggingAndCORSMiddleware(logger, mux),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// 4. Subir servidor HTTP em uma goroutine separada
	go func() {
		logger.Info("http server listening", slog.String("port", port))
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("http server error", slog.String("error", err.Error()))
			os.Exit(1)
		}
	}()

	// 5. Aguardar sinal de desligamento (Ctrl+C ou SIGTERM do Docker/Kubernetes)
	<-ctx.Done()
	logger.Info("shutdown signal received")

	// Encerra o servidor HTTP dando até 10s para requisições abertas concluírem
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("http server forced to shutdown", slog.String("error", err.Error()))
	}

	// Drena a fila do Worker Pool e aguarda goroutines ativas finalizarem
	enricher.Shutdown()
	logger.Info("application exited cleanly")
}

func loadEnvFile(filename string) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return
	}

	for line := range strings.SplitSeq(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if key, val, ok := strings.Cut(line, "="); ok {
			key = strings.TrimSpace(key)
			val = strings.Trim(strings.TrimSpace(val), `"'`)
			if os.Getenv(key) == "" {
				_ = os.Setenv(key, val)
			}
		}
	}
}

func getEnv(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}

func getEnvAsInt(key string, fallback int) int {
	if valStr := os.Getenv(key); valStr != "" {
		if val, err := strconv.Atoi(valStr); err == nil {
			return val
		}
	}
	return fallback
}
