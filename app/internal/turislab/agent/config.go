package agent

// Config carries the settings the agent service needs, decoupled from any
// config package: the caller maps its own YAML types onto these.
type Config struct {
	// MaxTurns caps the model rounds of a single chat request. 0 means the
	// default (12).
	MaxTurns int
	// Model selects and authenticates the LLM.
	Model ModelConfig
	// MCP lists external MCP servers (streamable HTTP) the agent may use.
	MCP []MCPServer
	// ArtifactsDir is the absolute folder where browser tools save files.
	// The instruction tells the model to name screenshots <id>.png here.
	ArtifactsDir string
	// Suggestions overrides the suggested prompts shown in the UI. Empty
	// uses the built-in defaults.
	Suggestions []string
}

// ModelConfig selects and authenticates the LLM behind the agent.
type ModelConfig struct {
	// Provider is "gemini" or "openai" (any OpenAI-compatible endpoint).
	Provider string
	// Name is the model name, e.g. gpt-4.1-mini.
	Name string
	// BaseURL points at an OpenAI-compatible endpoint (provider "openai"
	// only). Empty uses the provider default.
	BaseURL string
	// APIKey is the literal API key. Prefer APIKeyEnv so the secret stays
	// out of the configuration file.
	APIKey string
	// APIKeyEnv names the environment variable holding the API key.
	APIKeyEnv string
}

// MCPServer is one external MCP server the agent may call. When Command is
// set the server runs as a stdio subprocess (e.g. agent-browser mcp);
// otherwise the agent connects to URL over streamable HTTP.
type MCPServer struct {
	Name    string
	URL     string
	Headers map[string]string
	// Command runs a stdio MCP server. The first element is the executable
	// and the rest are its arguments. When set, URL/Headers are ignored.
	Command []string
	// Env adds environment variables (KEY=value) to a Command server.
	Env map[string]string
	// Tools, when non-empty, is an allowlist of tool names exposed to the
	// model and shown in the catalog. Empty means every tool the server
	// advertises.
	Tools []string
}
