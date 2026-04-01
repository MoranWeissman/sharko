package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/MoranWeissman/sharko/internal/ai"
	"github.com/MoranWeissman/sharko/internal/api"
	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/platform"
	"github.com/MoranWeissman/sharko/internal/service"
	"github.com/spf13/cobra"
)

func init() {
	serveCmd.Flags().Int("port", 8080, "HTTP server port")
	serveCmd.Flags().String("config", "config.yaml", "Path to config file (local mode)")
	serveCmd.Flags().String("static", "", "Path to static files directory (UI)")
	rootCmd.AddCommand(serveCmd)
}

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the Sharko API server",
	RunE: func(cmd *cobra.Command, args []string) error {
		port, _ := cmd.Flags().GetInt("port")
		configPath, _ := cmd.Flags().GetString("config")
		staticDir, _ := cmd.Flags().GetString("static")

		// Override from env
		if envPort := os.Getenv("AAP_PORT"); envPort != "" {
			fmt.Sscanf(envPort, "%d", &port)
		}
		if envConfig := os.Getenv("AAP_CONFIG"); envConfig != "" {
			configPath = envConfig
		}
		if envStatic := os.Getenv("AAP_STATIC_DIR"); envStatic != "" {
			staticDir = envStatic
		}

		// Load secrets from secrets.env for local development
		loadSecretsEnv("secrets.env")

		// Detect runtime mode
		mode := platform.Detect()
		log.Printf("Sharko starting in %s mode", mode)

		// Initialize config store
		var store config.Store
		switch mode {
		case platform.ModeKubernetes:
			encKey := os.Getenv("AAP_ENCRYPTION_KEY")
			if encKey == "" {
				return fmt.Errorf("AAP_ENCRYPTION_KEY is required when running on Kubernetes. " +
					"Set it in your Helm values (secrets.AAP_ENCRYPTION_KEY) or existingSecret")
			}
			secretName := os.Getenv("CONNECTION_SECRET_NAME")
			if secretName == "" {
				secretName = "aap-connections"
			}
			namespace := os.Getenv("AAP_NAMESPACE")
			if namespace == "" {
				namespace = "argocd-addons-platform"
			}
			var err error
			store, err = config.NewK8sStore(namespace, secretName, encKey)
			if err != nil {
				return fmt.Errorf("failed to create K8s connection store: %w", err)
			}
			log.Printf("Connection config stored in encrypted K8s Secret: %s/%s", namespace, secretName)
		default:
			store = config.NewFileStore(configPath)
			// Local dev is always dev mode (env var fallback for credentials)
			if os.Getenv("AAP_DEV_MODE") == "" {
				os.Setenv("AAP_DEV_MODE", "true")
			}
		}

		// AI configuration — resolve per-provider API key and model
		aiProvider := ai.Provider(os.Getenv("AI_PROVIDER"))
		aiAPIKey := os.Getenv("AI_API_KEY")    // generic fallback
		aiModel := os.Getenv("AI_CLOUD_MODEL") // generic fallback
		aiBaseURL := os.Getenv("AI_BASE_URL")
		aiAuthHeader := os.Getenv("AI_AUTH_HEADER")

		switch aiProvider {
		case ai.ProviderOpenAI:
			if k := os.Getenv("OPENAI_API_KEY"); k != "" {
				aiAPIKey = k
			}
			if m := os.Getenv("OPENAI_MODEL"); m != "" {
				aiModel = m
			}
		case ai.ProviderClaude:
			if k := os.Getenv("CLAUDE_API_KEY"); k != "" {
				aiAPIKey = k
			}
			if m := os.Getenv("CLAUDE_MODEL"); m != "" {
				aiModel = m
			}
		case ai.ProviderGemini:
			if k := os.Getenv("GEMINI_API_KEY"); k != "" {
				aiAPIKey = k
			}
			if m := os.Getenv("GEMINI_MODEL"); m != "" {
				aiModel = m
			}
		case ai.ProviderCustomOpenAI:
			if k := os.Getenv("CUSTOM_OPENAI_API_KEY"); k != "" {
				aiAPIKey = k
			}
			if m := os.Getenv("CUSTOM_OPENAI_MODEL"); m != "" {
				aiModel = m
			}
			if u := os.Getenv("CUSTOM_OPENAI_BASE_URL"); u != "" {
				aiBaseURL = u
			}
			if h := os.Getenv("CUSTOM_OPENAI_AUTH_HEADER"); h != "" {
				aiAuthHeader = h
			}
		}

		aiCfg := ai.Config{
			Provider:      aiProvider,
			OllamaURL:     getEnvDefault("AI_OLLAMA_URL", "http://localhost:11434"),
			OllamaModel:   getEnvDefault("AI_OLLAMA_MODEL", "llama3.2"),
			AgentModel:     os.Getenv("AI_AGENT_MODEL"),
			APIKey:         aiAPIKey,
			CloudModel:     aiModel,
			BaseURL:        aiBaseURL,
			AuthHeader:     aiAuthHeader,
			GitOpsEnabled:  os.Getenv("GITOPS_ACTIONS_ENABLED") == "true",
		}
		if v := os.Getenv("AI_MAX_ITERATIONS"); v != "" {
			fmt.Sscanf(v, "%d", &aiCfg.MaxIterations)
		}
		aiClient := ai.NewClient(aiCfg)
		if aiClient.IsEnabled() {
			model := aiCfg.OllamaModel
			if aiCfg.Provider == ai.ProviderClaude || aiCfg.Provider == ai.ProviderOpenAI || aiCfg.Provider == ai.ProviderGemini {
				model = aiCfg.CloudModel
			}
			log.Printf("AI provider enabled: %s (model: %s)", aiCfg.Provider, model)
		}

		// Wire up services
		connSvc := service.NewConnectionService(store)
		clusterSvc := service.NewClusterService()
		addonSvc := service.NewAddonService()
		dashboardSvc := service.NewDashboardService(connSvc)
		observabilitySvc := service.NewObservabilityService()
		upgradeSvc := service.NewUpgradeService(aiClient)

		// Build server
		srv := api.NewServer(connSvc, clusterSvc, addonSvc, dashboardSvc, observabilitySvc, upgradeSvc, aiClient)

		// AI config persistence (K8s mode — encrypted Secret)
		if mode == platform.ModeKubernetes {
			encKey := os.Getenv("AAP_ENCRYPTION_KEY")
			namespace := os.Getenv("AAP_NAMESPACE")
			if namespace == "" {
				namespace = "argocd-addons-platform"
			}
			if encKey != "" {
				aiStore, err := config.NewAIConfigStore(namespace, encKey)
				if err != nil {
					log.Printf("WARNING: Could not create AI config store: %v", err)
				} else {
					srv.SetAIConfigStore(aiStore)
					// Load persisted AI config (UI-set values override env vars)
					if savedJSON, err := aiStore.LoadJSON(); err != nil {
						log.Printf("WARNING: Could not load AI config: %v", err)
					} else if savedJSON != nil {
						var savedCfg ai.Config
						if err := json.Unmarshal(savedJSON, &savedCfg); err != nil {
							log.Printf("WARNING: Could not decode AI config: %v", err)
						} else if savedCfg.Provider != "" {
							aiClient.SetConfig(savedCfg)
							log.Printf("AI config loaded from K8s Secret (provider: %s)", savedCfg.Provider)
						}
					}
				}
			}
		}

		// Static files
		var staticFS fs.FS
		if staticDir != "" {
			if info, err := os.Stat(staticDir); err == nil && info.IsDir() {
				staticFS = os.DirFS(staticDir)
				log.Printf("Serving static files from %s", staticDir)
			}
		}

		router := api.NewRouter(srv, staticFS)

		addr := fmt.Sprintf(":%d", port)
		log.Printf("Listening on %s", addr)
		if err := http.ListenAndServe(addr, router); err != nil {
			return fmt.Errorf("server error: %w", err)
		}
		return nil
	},
}

func getEnvDefault(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

// loadSecretsEnv loads KEY=VALUE pairs from secrets.env into the environment.
// Lines starting with # and empty lines are skipped. Does not override existing env vars.
func loadSecretsEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return // file doesn't exist, that's fine
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	count := 0
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		// Remove surrounding quotes if present
		if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'')) {
			value = value[1 : len(value)-1]
		}
		// Don't override existing env vars
		if os.Getenv(key) == "" {
			os.Setenv(key, value)
			count++
		}
	}
	if count > 0 {
		log.Printf("Loaded %d secrets from %s", count, path)
	}
}
