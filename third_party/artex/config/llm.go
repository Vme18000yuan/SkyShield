package config

// DefaultContextWindowK is used when an LLM profile does not specify a window.
// K is decimal (1,000 tokens), matching the profile setting and UI.
const DefaultContextWindowK = 256

// DefaultContextWindowTokens is the corresponding window in tokens.
const DefaultContextWindowTokens = DefaultContextWindowK * 1000
