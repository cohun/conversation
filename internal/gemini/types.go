package gemini

// WebSocket outgoing message (Client -> Gemini Live)
type LiveClientMessage struct {
	Setup         *LiveSetup         `json:"setup,omitempty"`
	RealtimeInput *LiveRealtimeInput `json:"realtimeInput,omitempty"`
	ClientContent *LiveClientContent `json:"clientContent,omitempty"`
	ToolResponse  *LiveToolResponse  `json:"toolResponse,omitempty"`
}

type LiveSetup struct {
	Model               string                   `json:"model"`
	GenerationConfig    *LiveGenerationConfig    `json:"generationConfig,omitempty"`
	SystemInstruction   *LiveContent             `json:"systemInstruction,omitempty"`
	Tools               []LiveTool               `json:"tools,omitempty"`
	RealtimeInputConfig *LiveRealtimeInputConfig `json:"realtimeInputConfig,omitempty"`
}

type LiveRealtimeInputConfig struct {
	AutomaticActivityDetection *LiveAutomaticActivityDetection `json:"automaticActivityDetection,omitempty"`
}

type LiveAutomaticActivityDetection struct {
	Disabled bool `json:"disabled"`
}

type LiveGenerationConfig struct {
	ResponseModalities []string          `json:"responseModalities,omitempty"` // e.g. ["AUDIO"]
	SpeechConfig       *LiveSpeechConfig `json:"speechConfig,omitempty"`
}

type LiveSpeechConfig struct {
	VoiceConfig *LiveVoiceConfig `json:"voiceConfig,omitempty"`
}

type LiveVoiceConfig struct {
	PrebuiltVoiceConfig *LivePrebuiltVoiceConfig `json:"prebuiltVoiceConfig,omitempty"`
}

type LivePrebuiltVoiceConfig struct {
	VoiceName string `json:"voiceName,omitempty"` // Aoede, Puck, Charon, Fenrir, Kore
}

type LiveContent struct {
	Role  string     `json:"role,omitempty"`
	Parts []LivePart `json:"parts"`
}

type LivePart struct {
	Text       string    `json:"text,omitempty"`
	InlineData *LiveBlob `json:"inlineData,omitempty"`
}

type LiveBlob struct {
	MimeType string `json:"mimeType"` // e.g. "audio/pcm;rate=16000" or "audio/pcm;rate=24000"
	Data     string `json:"data"`     // Base64 encoded PCM
}

type LiveRealtimeInput struct {
	MediaChunks []LiveBlob `json:"mediaChunks,omitempty"`
}

type LiveClientContent struct {
	Turns        []LiveContent `json:"turns,omitempty"`
	TurnComplete bool          `json:"turnComplete,omitempty"`
}

type LiveTool struct {
	FunctionDeclarations []LiveFunctionDeclaration `json:"functionDeclarations,omitempty"`
}

type LiveFunctionDeclaration struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Parameters  interface{} `json:"parameters,omitempty"`
}

type LiveToolResponse struct {
	FunctionResponses []LiveFunctionResponse `json:"functionResponses,omitempty"`
}

type LiveFunctionResponse struct {
	ID       string                 `json:"id,omitempty"`
	Response map[string]interface{} `json:"response"`
}

// WebSocket incoming message (Gemini Live -> Client)
type LiveServerMessage struct {
	SetupComplete        *struct{}              `json:"setupComplete,omitempty"`
	ServerContent        *LiveServerContent     `json:"serverContent,omitempty"`
	ToolCall             *LiveToolCall          `json:"toolCall,omitempty"`
	ToolCallCancellation *LiveToolCancellation `json:"toolCallCancellation,omitempty"`
}

type LiveServerContent struct {
	ModelTurn    *LiveContent `json:"modelTurn,omitempty"`
	TurnComplete bool         `json:"turnComplete,omitempty"`
	Interrupted  bool         `json:"interrupted,omitempty"`
}

type LiveToolCall struct {
	FunctionCalls []LiveFunctionCall `json:"functionCalls,omitempty"`
}

type LiveFunctionCall struct {
	ID   string                 `json:"id"`
	Name string                 `json:"name"`
	Args map[string]interface{} `json:"args"`
}

type LiveToolCancellation struct {
	IDs []string `json:"ids"`
}

// REST models query response
type ModelListResponse struct {
	Models []ModelInfo `json:"models"`
}

type ModelInfo struct {
	Name                       string   `json:"name"`
	Version                    string   `json:"version"`
	DisplayName                string   `json:"displayName"`
	Description                string   `json:"description"`
	SupportedGenerationMethods []string `json:"supportedGenerationMethods"`
}
